package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	identityCollectionName    = "_m_stash_identities"
	oauthTicketCollectionName = "_m_stash_oauth_tickets"

	// oauthStateAudience scopes the short-lived state JWT so authenticate() (which
	// requires the deployment's own audience) can never accept it as a session.
	oauthStateAudience   = "m-stash-oauth-state"
	oauthStateTTL        = 5 * time.Minute
	oauthStateCookieName = "m_stash_oauth"
	// The state cookie is bound to the OAuth routes only, so it never rides along
	// with ordinary API or UI requests.
	oauthStateCookiePath = "/v1/auth/oauth"

	// Callback redirects carry only a single-use ticket, never a JWT. Tickets are
	// stored as SHA-256 hashes and expire after one minute.
	oauthTicketPrefix = "otc_"
	oauthTicketTTL    = 60 * time.Second

	oauthCodeVerifierBytes = 32
	oauthNonceBytes        = 16
	oauthTicketBytes       = 32
)

var (
	errOAuthLinkRequired    = errors.New("account exists; sign in with your password to link this provider")
	errOAuthLinkConflict    = errors.New("this identity is already linked to an account")
	errOAuthEmailUnverified = errors.New("the provider did not return a verified email")
	errOAuthUserLimit       = errors.New("this application reached its plan limit for user accounts")
	errOAuthStateInvalid    = errors.New("invalid oauth state")
	errOAuthRedirectBlocked = errors.New("redirect_uri is not an allowed origin")
)

// VerifiedIdentity is the normalized, provider-agnostic result of a completed
// OAuth exchange. Everything the rest of m-stash needs to mint a session is here.
type VerifiedIdentity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	AvatarURL     string
	UsernameHint  string
}

// OAuthAdapter is the single, narrow boundary every external identity provider
// implements. Adapters only translate a provider's protocol into a
// VerifiedIdentity; they never touch sessions, MongoDB, or the HTTP layer.
type OAuthAdapter interface {
	Name() string
	AuthorizeURL(callbackURL, state, codeChallenge string) string
	Exchange(ctx context.Context, callbackURL, code, codeVerifier string) (VerifiedIdentity, error)
}

// UserIdentity links an m-stash user to one external provider account. Accounts
// are keyed by the immutable (appId, provider, subject) triple, never by email.
type UserIdentity struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"-"`
	AppID       bson.ObjectID `bson:"appId" json:"-"`
	UserID      bson.ObjectID `bson:"userId" json:"-"`
	Provider    string        `bson:"provider" json:"provider"`
	Subject     string        `bson:"subject" json:"-"`
	Email       string        `bson:"email,omitempty" json:"email,omitempty"`
	DisplayName string        `bson:"displayName,omitempty" json:"displayName,omitempty"`
	AvatarURL   string        `bson:"avatarUrl,omitempty" json:"avatarUrl,omitempty"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
	LastLoginAt time.Time     `bson:"lastLoginAt" json:"lastLoginAt"`
}

// oauthTicket is the 60-second, single-use handoff between the callback redirect
// and the SPA's exchange call, so a JWT never travels through a URL.
type oauthTicket struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TokenHash string        `bson:"tokenHash"`
	AppID     bson.ObjectID `bson:"appId"`
	UserID    bson.ObjectID `bson:"userId"`
	IsNewUser bool          `bson:"isNewUser"`
	CreatedAt time.Time     `bson:"createdAt"`
	ExpiresAt time.Time     `bson:"expiresAt"`
}

// oauthStateClaims is sealed into the signed state JWT and cross-checked against
// the HttpOnly nonce cookie to defeat login-CSRF and bind the PKCE verifier.
type oauthStateClaims struct {
	Nonce       string `json:"nonce"`
	AppID       string `json:"appId"`
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirectUri"`
	Verifier    string `json:"verifier"`
	LinkUserID  string `json:"linkUserId,omitempty"`
	jwt.RegisteredClaims
}

func ensureOAuthIndexes(ctx context.Context, database *mongo.Database) error {
	if _, err := database.Collection(identityCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "appId", Value: 1}, {Key: "provider", Value: 1}, {Key: "subject", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "provider", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("create identity indexes: %w", err)
	}
	if _, err := database.Collection(oauthTicketCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tokenHash", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	}); err != nil {
		return fmt.Errorf("create oauth ticket indexes: %w", err)
	}
	return nil
}

// oauthProviderNames lists the configured adapters in a stable order.
func (app *application) oauthProviderNames() []string {
	names := make([]string, 0, len(app.oauthAdapters))
	for name := range app.oauthAdapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- PKCE, nonce, and ticket primitives -----------------------------------

func newCodeVerifier() (string, error) {
	buffer := make([]byte, oauthCodeVerifierBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newOAuthNonce() (string, error) {
	buffer := make([]byte, oauthNonceBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func newOAuthTicketToken() (string, error) {
	buffer := make([]byte, oauthTicketBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return oauthTicketPrefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashOAuthTicket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// --- State signing & cookie ------------------------------------------------

func (app *application) signOAuthState(claims oauthStateClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(app.config.JWTSecret))
}

func (app *application) parseOAuthState(stateString string) (*oauthStateClaims, error) {
	if stateString == "" {
		return nil, errOAuthStateInvalid
	}
	claims := &oauthStateClaims{}
	token, err := jwt.ParseWithClaims(stateString, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}
		return []byte(app.config.JWTSecret), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(app.config.JWTIssuer),
		jwt.WithAudience(oauthStateAudience),
	)
	if err != nil || !token.Valid || claims.Nonce == "" {
		return nil, errOAuthStateInvalid
	}
	return claims, nil
}

func (app *application) oauthStateCookie(nonce string) *http.Cookie {
	return &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    nonce,
		Path:     oauthStateCookiePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		Expires:  time.Now().Add(oauthStateTTL),
		HttpOnly: true,
		Secure:   app.config.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (app *application) clearOAuthStateCookie(w http.ResponseWriter) {
	cookie := app.oauthStateCookie("")
	cookie.MaxAge = -1
	cookie.Expires = time.Now().Add(-time.Hour)
	http.SetCookie(w, cookie)
}

func (app *application) oauthCallbackURL(r *http.Request, provider string) string {
	return app.publicBaseURL(r) + oauthStateCookiePath + "/" + provider + "/callback"
}

// --- redirect_uri allowlisting --------------------------------------------

// validateOAuthRedirect returns a safe post-login redirect target, rejecting any
// origin that isn't this deployment, an explicit allowed origin, or a localhost
// dev origin for sandboxes / wildcard-CORS apps. A wildcard never permits an
// arbitrary external redirect host.
func (app *application) validateOAuthRedirect(r *http.Request, record *AppRecord, redirectURI string) (string, error) {
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return app.publicBaseURL(r) + "/login", nil
	}
	origin, parsed, err := originOf(redirectURI)
	if err != nil {
		return "", errOAuthRedirectBlocked
	}
	if strings.EqualFold(origin, app.publicBaseURL(r)) {
		return redirectURI, nil
	}
	wildcard := false
	candidates := append(append([]string{}, record.AllowedOrigins...), app.config.AllowedOrigins...)
	for _, allowed := range candidates {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == "*" {
			wildcard = true
			continue
		}
		if strings.EqualFold(strings.TrimRight(allowed, "/"), origin) {
			return redirectURI, nil
		}
	}
	if (record.Sandbox || wildcard) && isLocalhostOrigin(parsed) {
		return redirectURI, nil
	}
	return "", errOAuthRedirectBlocked
}

func originOf(raw string) (string, *url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", nil, errOAuthRedirectBlocked
	}
	return parsed.Scheme + "://" + parsed.Host, parsed, nil
}

func isLocalhostOrigin(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "http" {
		return false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func appendQuery(base, key, value string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// --- Identity resolution & provisioning ------------------------------------

func newUserIdentity(appID, userID bson.ObjectID, verified VerifiedIdentity, now time.Time) UserIdentity {
	return UserIdentity{
		ID:          bson.NewObjectID(),
		AppID:       appID,
		UserID:      userID,
		Provider:    verified.Provider,
		Subject:     verified.Subject,
		Email:       verified.Email,
		DisplayName: verified.DisplayName,
		AvatarURL:   verified.AvatarURL,
		CreatedAt:   now,
		LastLoginAt: now,
	}
}

func (app *application) loadUserByID(ctx context.Context, appID, userID bson.ObjectID) (User, error) {
	var user User
	if err := app.database.Collection("_users").FindOne(ctx, bson.M{"_id": userID, "appId": appID}).Decode(&user); err != nil {
		return User{}, err
	}
	return user, nil
}

// resolveOrProvisionExternalUser converges every OAuth outcome onto a single
// m-stash User: returning login, explicit link, safe auto-link for OAuth-only
// accounts, or first-time provisioning. It never merges into a password account.
func (app *application) resolveOrProvisionExternalUser(ctx context.Context, record *AppRecord, verified VerifiedIdentity, linkUserID string) (User, bool, error) {
	identities := app.database.Collection(identityCollectionName)

	// 1. A known identity is a returning login.
	var existing UserIdentity
	err := identities.FindOne(ctx, bson.M{"appId": record.ID, "provider": verified.Provider, "subject": verified.Subject}).Decode(&existing)
	if err == nil {
		if linkUserID != "" && existing.UserID.Hex() != linkUserID {
			return User{}, false, errOAuthLinkConflict
		}
		_, _ = identities.UpdateByID(ctx, existing.ID, bson.M{"$set": bson.M{"lastLoginAt": time.Now().UTC()}})
		user, loadErr := app.loadUserByID(ctx, record.ID, existing.UserID)
		if loadErr != nil {
			return User{}, false, loadErr
		}
		return user, false, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, false, err
	}

	// 2. Explicit link requested by an already-authenticated user.
	if linkUserID != "" {
		userID, parseErr := bson.ObjectIDFromHex(linkUserID)
		if parseErr != nil {
			return User{}, false, errOAuthStateInvalid
		}
		user, loadErr := app.loadUserByID(ctx, record.ID, userID)
		if loadErr != nil {
			return User{}, false, loadErr
		}
		if linkErr := app.linkIdentity(ctx, record.ID, userID, verified); linkErr != nil {
			return User{}, false, linkErr
		}
		return user, false, nil
	}

	// 3. Unauthenticated first login: a verified email is mandatory.
	if verified.Email == "" || !verified.EmailVerified {
		return User{}, false, errOAuthEmailUnverified
	}

	// 3a. An account already owns this email.
	var existingUser User
	userErr := app.database.Collection("_users").FindOne(ctx, bson.M{"appId": record.ID, "email": verified.Email}).Decode(&existingUser)
	if userErr == nil {
		// Password signups are unverified, so never silently adopt one.
		if existingUser.PasswordHash != "" {
			return User{}, false, errOAuthLinkRequired
		}
		// An OAuth-only account with a matching verified email is safe to extend.
		if linkErr := app.linkIdentity(ctx, record.ID, existingUser.ID, verified); linkErr != nil {
			return User{}, false, linkErr
		}
		return existingUser, false, nil
	}
	if !errors.Is(userErr, mongo.ErrNoDocuments) {
		return User{}, false, userErr
	}

	// 3b. Brand-new external user.
	user, provisionErr := app.provisionExternalUser(ctx, record, verified)
	if provisionErr != nil {
		return User{}, false, provisionErr
	}
	return user, true, nil
}

func (app *application) linkIdentity(ctx context.Context, appID, userID bson.ObjectID, verified VerifiedIdentity) error {
	identity := newUserIdentity(appID, userID, verified, time.Now().UTC())
	if _, err := app.database.Collection(identityCollectionName).InsertOne(ctx, identity); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return errOAuthLinkConflict
		}
		return err
	}
	return nil
}

func (app *application) provisionExternalUser(ctx context.Context, record *AppRecord, verified VerifiedIdentity) (User, error) {
	if limit := effectiveLimits(record).MaxUsers; limit > 0 {
		count, err := app.database.Collection("_users").CountDocuments(ctx, bson.M{"appId": record.ID})
		if err != nil {
			return User{}, err
		}
		if count >= int64(limit) {
			return User{}, errOAuthUserLimit
		}
	}

	base := baseSlugFromHint(verified.UsernameHint, verified.Email)
	now := time.Now().UTC()
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		slug := base
		if attempt > 0 {
			slug = slugWithSuffix(base)
		}
		if validateNamespaceSlug(slug) != nil {
			base = "user"
			slug = slugWithSuffix(base)
		}
		user := User{
			ID:        bson.NewObjectID(),
			AppID:     record.ID,
			Email:     verified.Email,
			Role:      "user",
			CreatedAt: now,
		}
		namespace := newPersonalNamespace(record.ID, user.ID, slug, now)
		identity := newUserIdentity(record.ID, user.ID, verified, now)
		err := app.createExternalAccount(ctx, user, namespace, identity, !record.Sandbox)
		if err == nil {
			return user, nil
		}
		if mongo.IsDuplicateKeyError(err) {
			// A slug (or racing email) collision — retry with a fresh suffix.
			lastErr = err
			continue
		}
		return User{}, err
	}
	return User{}, lastErr
}

func (app *application) createExternalAccount(ctx context.Context, user User, namespace Namespace, identity UserIdentity, seedWelcome bool) error {
	session, err := app.mongoClient.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, err := app.database.Collection("_users").InsertOne(transactionContext, user); err != nil {
			return nil, err
		}
		if _, err := app.database.Collection(namespaceCollectionName).InsertOne(transactionContext, namespace); err != nil {
			return nil, err
		}
		membership := newOwnerMembership(namespace.AppID, namespace.ID, user.ID, namespace.CreatedAt)
		if _, err := app.database.Collection(namespaceMembershipCollection).InsertOne(transactionContext, membership); err != nil {
			return nil, err
		}
		if _, err := app.database.Collection(identityCollectionName).InsertOne(transactionContext, identity); err != nil {
			return nil, err
		}
		if seedWelcome {
			seed := newWelcomeResource(namespace.AppID, namespace.ID, user.ID, namespace.Slug, namespace.CreatedAt)
			if _, err := app.database.Collection(resourceCollectionName).InsertOne(transactionContext, seed); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	return err
}

// baseSlugFromHint derives a valid namespace slug from the provider's username
// hint, falling back to the email local-part and finally a generic "user".
func baseSlugFromHint(hint, email string) string {
	if candidate := sanitizeSlug(hint); validateNamespaceSlug(candidate) == nil {
		return candidate
	}
	if at := strings.IndexByte(email, '@'); at > 0 {
		if candidate := sanitizeSlug(email[:at]); validateNamespaceSlug(candidate) == nil {
			return candidate
		}
	}
	return "user"
}

// sanitizeSlug reduces arbitrary text to the lowercase-alphanumeric-with-single-
// separator shape validateNamespaceSlug accepts (without guaranteeing length).
func sanitizeSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	lastSeparator := false
	for _, character := range raw {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
			lastSeparator = false
		case character == '-' || character == '_' || character == '.' || character == ' ':
			if builder.Len() > 0 && !lastSeparator {
				builder.WriteByte('-')
				lastSeparator = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-_")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-_")
	}
	return slug
}

// slugWithSuffix appends a 4-char random suffix, trimming the base so the result
// stays within the 32-char namespace limit.
func slugWithSuffix(base string) string {
	if validateNamespaceSlug(base) != nil {
		base = "user"
	}
	if len(base) > 27 {
		base = strings.Trim(base[:27], "-_")
		if base == "" {
			base = "user"
		}
	}
	return base + "-" + randomSlugSuffix()
}

func randomSlugSuffix() string {
	buffer := make([]byte, 2)
	if _, err := rand.Read(buffer); err != nil {
		return "0000"
	}
	return hex.EncodeToString(buffer)
}

// --- One-time ticket lifecycle ---------------------------------------------

func (app *application) issueOAuthTicket(ctx context.Context, record *AppRecord, user User, isNewUser bool) (string, error) {
	token, err := newOAuthTicketToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	ticket := oauthTicket{
		ID:        bson.NewObjectID(),
		TokenHash: hashOAuthTicket(token),
		AppID:     record.ID,
		UserID:    user.ID,
		IsNewUser: isNewUser,
		CreatedAt: now,
		ExpiresAt: now.Add(oauthTicketTTL),
	}
	if _, err := app.database.Collection(oauthTicketCollectionName).InsertOne(ctx, ticket); err != nil {
		return "", err
	}
	return token, nil
}

func (app *application) consumeOAuthTicket(ctx context.Context, record *AppRecord, token string) (oauthTicket, error) {
	var ticket oauthTicket
	err := app.database.Collection(oauthTicketCollectionName).FindOneAndDelete(
		ctx,
		bson.M{"tokenHash": hashOAuthTicket(token), "appId": record.ID},
	).Decode(&ticket)
	if err != nil {
		return oauthTicket{}, err
	}
	if time.Now().UTC().After(ticket.ExpiresAt) {
		return oauthTicket{}, mongo.ErrNoDocuments
	}
	return ticket, nil
}

func oauthErrorCode(err error) string {
	switch {
	case errors.Is(err, errOAuthLinkRequired):
		return "account_exists_link_required"
	case errors.Is(err, errOAuthLinkConflict):
		return "identity_already_linked"
	case errors.Is(err, errOAuthEmailUnverified):
		return "email_unverified"
	case errors.Is(err, errOAuthUserLimit):
		return "app_full"
	default:
		return "server_error"
	}
}

// --- HTTP handlers ---------------------------------------------------------

// handleOAuthProviders lists the deployment's configured adapters so the UI can
// decide which social buttons to render.
func (app *application) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"providers": app.oauthProviderNames()}})
}

// handleOAuth dispatches the browser-facing {provider}/start and
// {provider}/callback legs of the redirect flow.
func (app *application) handleOAuth(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, oauthStateCookiePath+"/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "Unknown OAuth route")
		return
	}
	provider, action := parts[0], parts[1]
	switch action {
	case "start":
		app.handleOAuthStart(w, r, provider)
	case "callback":
		app.handleOAuthCallback(w, r, provider)
	default:
		writeError(w, http.StatusNotFound, "Unknown OAuth route")
	}
}

func (app *application) handleOAuthStart(w http.ResponseWriter, r *http.Request, provider string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := app.oauthAdapters[provider]; !ok {
		writeError(w, http.StatusNotFound, "Unknown or disabled identity provider")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	key := appKeyFromRequest(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "Missing "+appKeyHeader+" application key")
		return
	}
	record, err := app.lookupApp(ctx, key)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unknown application key")
		return
	}
	authorizeURL, err := app.prepareOAuthRedirect(w, r, provider, record, "", r.URL.Query().Get("redirect_uri"))
	if err != nil {
		if errors.Is(err, errOAuthRedirectBlocked) {
			writeError(w, http.StatusBadRequest, errOAuthRedirectBlocked.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not start sign-in")
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// prepareOAuthRedirect validates the redirect target, seals a signed state JWT
// bound to an HttpOnly nonce cookie and PKCE verifier, and returns the provider
// authorize URL. No database writes occur here.
func (app *application) prepareOAuthRedirect(w http.ResponseWriter, r *http.Request, provider string, record *AppRecord, linkUserID, redirectURIRaw string) (string, error) {
	adapter, ok := app.oauthAdapters[provider]
	if !ok {
		return "", errOAuthStateInvalid
	}
	redirectURI, err := app.validateOAuthRedirect(r, record, redirectURIRaw)
	if err != nil {
		return "", err
	}
	verifier, err := newCodeVerifier()
	if err != nil {
		return "", err
	}
	nonce, err := newOAuthNonce()
	if err != nil {
		return "", err
	}
	now := time.Now()
	state := oauthStateClaims{
		Nonce:       nonce,
		AppID:       record.ID.Hex(),
		Provider:    provider,
		RedirectURI: redirectURI,
		Verifier:    verifier,
		LinkUserID:  linkUserID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    app.config.JWTIssuer,
			Audience:  jwt.ClaimStrings{oauthStateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(oauthStateTTL)),
		},
	}
	signed, err := app.signOAuthState(state)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, app.oauthStateCookie(nonce))
	return adapter.AuthorizeURL(app.oauthCallbackURL(r, provider), signed, codeChallengeS256(verifier)), nil
}

func (app *application) handleOAuthCallback(w http.ResponseWriter, r *http.Request, provider string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	adapter, ok := app.oauthAdapters[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "Unknown or disabled identity provider")
		return
	}

	claims, err := app.parseOAuthState(r.URL.Query().Get("state"))
	if err != nil {
		app.clearOAuthStateCookie(w)
		writeError(w, http.StatusBadRequest, "Invalid or expired OAuth state")
		return
	}
	// The signed state must agree with the path and with the nonce cookie, then
	// the single-use cookie is cleared regardless of outcome.
	cookie, cookieErr := r.Cookie(oauthStateCookieName)
	app.clearOAuthStateCookie(w)
	if claims.Provider != provider {
		writeError(w, http.StatusBadRequest, "OAuth state does not match provider")
		return
	}
	if cookieErr != nil || cookie.Value == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(claims.Nonce)) != 1 {
		writeError(w, http.StatusBadRequest, "Missing or mismatched OAuth state")
		return
	}

	if providerErr := strings.TrimSpace(r.URL.Query().Get("error")); providerErr != "" {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "provider_denied")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "missing_code")
		return
	}

	appID, err := bson.ObjectIDFromHex(claims.AppID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid OAuth state")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	record, err := app.lookupAppByID(ctx, appID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unknown application")
		return
	}

	verified, err := adapter.Exchange(ctx, app.oauthCallbackURL(r, provider), code, claims.Verifier)
	if err != nil {
		app.logger.Error("oauth exchange failed", "provider", provider, "request_id", requestIDFromContext(ctx), "error", err)
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "exchange_failed")
		return
	}
	verified.Provider = provider

	user, isNewUser, err := app.resolveOrProvisionExternalUser(ctx, record, verified, claims.LinkUserID)
	if err != nil {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", oauthErrorCode(err))
		return
	}
	ticket, err := app.issueOAuthTicket(ctx, record, user, isNewUser)
	if err != nil {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "server_error")
		return
	}
	app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_code", ticket)
}

func (app *application) redirectOAuthResult(w http.ResponseWriter, r *http.Request, redirectURI, key, value string) {
	http.Redirect(w, r, appendQuery(redirectURI, key, value), http.StatusFound)
}

func (app *application) lookupAppByID(ctx context.Context, appID bson.ObjectID) (*AppRecord, error) {
	var record AppRecord
	if err := app.database.Collection(appsCollectionName).FindOne(ctx, bson.M{"_id": appID, "active": true}).Decode(&record); err != nil {
		return nil, err
	}
	if record.expired(time.Now().UTC()) {
		return nil, mongo.ErrNoDocuments
	}
	return &record, nil
}

// handleOAuthExchange trades a one-time ticket for an m-stash JWT and browser
// session, mirroring the login/signup payload. It is app-scoped via resolveApp.
func (app *application) handleOAuthExchange(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if !decodeJSONBody(w, r, &request, false) {
		return
	}
	request.Code = strings.TrimSpace(request.Code)
	if !strings.HasPrefix(request.Code, oauthTicketPrefix) {
		writeError(w, http.StatusBadRequest, "Invalid or expired code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	record, ok := appFromContext(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "Missing application context")
		return
	}
	ticket, err := app.consumeOAuthTicket(ctx, record, request.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid or expired code")
		return
	}
	user, err := app.loadUserByID(ctx, record.ID, ticket.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Account is no longer available")
		return
	}
	token, err := app.generateJWT(user.ID.Hex(), user.Email, user.Role, user.AppID.Hex())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	if err := app.issueBrowserSession(ctx, w, user, bson.NilObjectID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create browser session")
		return
	}
	payload := map[string]any{"token": token, "user": user, "isNewUser": ticket.IsNewUser}
	if namespace, nsErr := app.personalNamespaceForClaims(ctx, &Claims{UID: user.ID.Hex()}); nsErr == nil {
		payload["namespace"] = namespace
	}
	writeJSON(w, http.StatusOK, payload)
}

// --- Authenticated identity management -------------------------------------

func (app *application) handleMeIdentities(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	appID, ok := appIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "Missing application context")
		return
	}
	userID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid authenticated user")
		return
	}
	cursor, err := app.database.Collection(identityCollectionName).Find(ctx, bson.M{"appId": appID, "userId": userID})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not list identities")
		return
	}
	// A non-nil slice serializes an empty result as [] rather than null, so the
	// UI can map over it directly.
	docs := []UserIdentity{}
	if err := cursor.All(ctx, &docs); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not list identities")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"identities": docs}})
}

func (app *application) handleMeIdentityActions(w http.ResponseWriter, r *http.Request, claims *Claims) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/me/identities/"), "/")
	switch {
	case rest == "start":
		app.handleMeIdentityStart(w, r, claims)
	case rest != "" && !strings.Contains(rest, "/"):
		app.handleMeIdentityUnlink(w, r, claims, rest)
	default:
		writeError(w, http.StatusNotFound, "Unknown identity route")
	}
}

// handleMeIdentityStart begins an account-linking flow for the signed-in user,
// sealing their user id into the OAuth state and returning the authorize URL.
func (app *application) handleMeIdentityStart(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if _, ok := app.oauthAdapters[provider]; !ok {
		writeError(w, http.StatusNotFound, "Unknown or disabled identity provider")
		return
	}
	record, ok := appFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusBadRequest, "Missing application context")
		return
	}
	authorizeURL, err := app.prepareOAuthRedirect(w, r, provider, record, claims.UID, r.URL.Query().Get("redirect_uri"))
	if err != nil {
		if errors.Is(err, errOAuthRedirectBlocked) {
			writeError(w, http.StatusBadRequest, errOAuthRedirectBlocked.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not start provider linking")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"authorizeUrl": authorizeURL}})
}

// handleMeIdentityUnlink removes a linked provider, refusing to strip a user's
// only remaining way to sign in.
func (app *application) handleMeIdentityUnlink(w http.ResponseWriter, r *http.Request, claims *Claims, provider string) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	appID, ok := appIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusBadRequest, "Missing application context")
		return
	}
	userID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid authenticated user")
		return
	}
	user, err := app.loadUserByID(ctx, appID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Account not found")
		return
	}
	identities := app.database.Collection(identityCollectionName)
	count, err := identities.CountDocuments(ctx, bson.M{"appId": appID, "userId": userID})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not update identities")
		return
	}
	if user.PasswordHash == "" && count <= 1 {
		writeError(w, http.StatusConflict, "Cannot unlink your only sign-in method. Set a password or link another provider first.")
		return
	}
	result, err := identities.DeleteOne(ctx, bson.M{"appId": appID, "userId": userID, "provider": provider})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not update identities")
		return
	}
	if result.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Provider is not linked")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"provider": provider, "unlinked": true}})
}

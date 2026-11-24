package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

// Builder console social login. It reuses the app OAuth machinery — the same
// registered provider callback, signed state, PKCE, nonce cookie, and one-time
// ticket — with the nil app id standing for the control plane. Tickets are
// scoped by app id, so a console ticket can never be redeemed by an app and
// vice versa.

var (
	errBuilderSignupClosed = errors.New("builder signup is closed on this deployment")
	errBuilderSignupFull   = errors.New("builder signups are at capacity for today")
)

// controlPlane stands in for "no app" wherever the OAuth helpers key on an app.
var controlPlane = &AppRecord{ID: bson.NilObjectID}

// consoleReturnPath keeps only a same-origin /console URL (with its query), so
// the post-login redirect can never leave the deployment.
func consoleReturnPath(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path != "/console" {
		return "/console"
	}
	if parsed.RawQuery == "" {
		return "/console"
	}
	return "/console?" + parsed.Query().Encode()
}

func (app *application) handleBuilderOAuthStart(w http.ResponseWriter, r *http.Request, provider string) {
	redirectURI := app.publicBaseURL(r) + consoleReturnPath(r.URL.Query().Get("next"))
	authorizeURL, err := app.sealOAuthRedirect(w, r, app.oauthAdapters[provider], provider, bson.NilObjectID.Hex(), "", redirectURI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start sign-in")
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (app *application) finishBuilderOAuth(ctx context.Context, w http.ResponseWriter, r *http.Request, adapter OAuthAdapter, provider string, claims *oauthStateClaims, code string) {
	verified, err := adapter.Exchange(ctx, app.oauthCallbackURL(r, provider), code, claims.Verifier)
	if err != nil {
		app.logger.Error("builder oauth exchange failed", "provider", provider, "request_id", requestIDFromContext(ctx), "error", err)
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "exchange_failed")
		return
	}
	verified.Provider = provider
	builder, isNew, err := app.resolveOrProvisionBuilder(ctx, verified)
	if err != nil {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", oauthErrorCode(err))
		return
	}
	ticket, err := app.issueOAuthTicket(ctx, controlPlane, builder, isNew)
	if err != nil {
		app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_error", "server_error")
		return
	}
	app.redirectOAuthResult(w, r, claims.RedirectURI, "oauth_code", ticket)
}

func (app *application) loadControlPlaneUser(ctx context.Context, filter bson.M) (User, error) {
	filter["appId"] = bson.NilObjectID
	filter["role"] = bson.M{"$in": bson.A{builderRole, operatorRole}}
	var user User
	err := app.database.Collection("_users").FindOne(ctx, filter).Decode(&user)
	return user, err
}

// resolveOrProvisionBuilder maps a verified provider identity onto a builder:
// a linked identity signs in; an OAuth-only builder with the same verified email
// gains the identity; a password builder is never adopted (signup emails are
// unverified, so that would hand the account to whoever registered it first);
// otherwise a new free-plan builder is created, subject to the signup controls.
func (app *application) resolveOrProvisionBuilder(ctx context.Context, verified VerifiedIdentity) (User, bool, error) {
	identities := app.database.Collection(identityCollectionName)
	var existing UserIdentity
	err := identities.FindOne(ctx, bson.M{"appId": bson.NilObjectID, "provider": verified.Provider, "subject": verified.Subject}).Decode(&existing)
	if err == nil {
		builder, loadErr := app.loadControlPlaneUser(ctx, bson.M{"_id": existing.UserID})
		if loadErr == nil {
			_, _ = identities.UpdateByID(ctx, existing.ID, bson.M{"$set": bson.M{"lastLoginAt": time.Now().UTC()}})
			return builder, false, nil
		}
		if !errors.Is(loadErr, mongo.ErrNoDocuments) {
			return User{}, false, loadErr
		}
		// The builder was deleted: drop the stale link and treat this as a
		// first sign-in instead of locking the provider account out.
		if _, delErr := identities.DeleteOne(ctx, bson.M{"_id": existing.ID}); delErr != nil {
			return User{}, false, delErr
		}
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, false, err
	}

	if verified.Email == "" || !verified.EmailVerified {
		return User{}, false, errOAuthEmailUnverified
	}
	verified.Email = normalizeEmail(verified.Email)
	if builder, findErr := app.loadControlPlaneUser(ctx, bson.M{"email": verified.Email}); findErr == nil {
		if builder.PasswordHash != "" {
			return User{}, false, errOAuthLinkRequired
		}
		if linkErr := app.linkIdentity(ctx, bson.NilObjectID, builder.ID, verified); linkErr != nil {
			return User{}, false, linkErr
		}
		return builder, false, nil
	} else if !errors.Is(findErr, mongo.ErrNoDocuments) {
		return User{}, false, findErr
	}

	settings := app.effectiveSettings(ctx)
	if !settings.BuilderSignupOpen {
		return User{}, false, errBuilderSignupClosed
	}
	if app.builderSignupDailyLimiter != nil {
		allowed, limitErr := app.builderSignupDailyLimiter.allowWithLimit(ctx, "builder-signup-global", "global", settings.BuilderSignupDailyLimit)
		if limitErr != nil {
			return User{}, false, limitErr
		}
		if !allowed {
			return User{}, false, errBuilderSignupFull
		}
	}
	builder := User{
		ID:        bson.NewObjectID(),
		AppID:     bson.NilObjectID,
		Email:     verified.Email,
		Role:      builderRole,
		Plan:      freePlan,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := app.database.Collection("_users").InsertOne(ctx, builder); err != nil {
		return User{}, false, err
	}
	// If linking fails the builder is OAuth-only with this verified email, so
	// the next attempt adopts it through the branch above.
	if err := app.linkIdentity(ctx, bson.NilObjectID, builder.ID, verified); err != nil {
		return User{}, false, err
	}
	app.metrics.recordBuilderSignup()
	return builder, true, nil
}

// handleBuilderOAuthExchange trades a console ticket for a builder session,
// mirroring the password login response.
func (app *application) handleBuilderOAuthExchange(w http.ResponseWriter, r *http.Request) {
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
	ticket, err := app.consumeOAuthTicket(ctx, controlPlane, request.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid or expired code")
		return
	}
	builder, err := app.loadControlPlaneUser(ctx, bson.M{"_id": ticket.UserID})
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Account is no longer available")
		return
	}
	token, err := app.generateJWT(builder.ID.Hex(), builder.Email, builder.Role, bson.NilObjectID.Hex())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	if err := app.issueBrowserSession(ctx, w, builder, bson.NilObjectID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create builder session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"isNewUser": ticket.IsNewUser,
		"builder":   map[string]any{"id": builder.ID.Hex(), "email": builder.Email, "operator": builder.Role == operatorRole, "plan": builder.Plan},
	})
}

// ---------------------------------------------------------------------------
// Owner login: the app-level admin a builder signs in as inside their app.
// ---------------------------------------------------------------------------

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

// sandboxOwnerDomain marks the throwaway admin a vibe sandbox is created with.
const sandboxOwnerDomain = "@sandbox.invalid"

// builderAppOwner lists (GET) or sets (PUT {email, password}) an owned app's
// owner login. Setting it replaces the generated sandbox owner when that is the
// app's only admin, updates an existing admin with the same email, or adds a
// new admin; the password is never returned and old sessions are revoked.
func (app *application) builderAppOwner(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.ownedApp(ctx, ownerID, appID); err != nil {
		app.writeOwnedAppLookupError(w, err, "Could not load application")
		return
	}
	users := app.database.Collection("_users")

	listAdmins := func() ([]User, error) {
		cursor, err := users.Find(ctx, bson.M{"appId": appID, "role": "admin"})
		if err != nil {
			return nil, err
		}
		var admins []User
		err = cursor.All(ctx, &admins)
		return admins, err
	}

	switch r.Method {
	case http.MethodGet:
		admins, err := listAdmins()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not read owner login")
			return
		}
		emails := make([]string, 0, len(admins))
		for _, admin := range admins {
			emails = append(emails, admin.Email)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"emails": emails}})
	case http.MethodPut:
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		input.Email = normalizeEmail(input.Email)
		if err := validateSignUpCredentials(input.Email, input.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.HasSuffix(input.Email, sandboxOwnerDomain) {
			writeError(w, http.StatusBadRequest, "Use an email address you can actually sign in with")
			return
		}
		passwordHash, err := hashPassword(input.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Error hashing password")
			return
		}

		var target User
		findErr := users.FindOne(ctx, bson.M{"appId": appID, "email": input.Email}).Decode(&target)
		switch {
		case findErr == nil && target.Role != "admin":
			writeError(w, http.StatusConflict, "A member of this app already uses that email")
			return
		case findErr == nil:
			// Same email as an existing admin: just set the password.
		case errors.Is(findErr, mongo.ErrNoDocuments):
			admins, err := listAdmins()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Could not update owner login")
				return
			}
			if len(admins) == 1 && strings.HasSuffix(admins[0].Email, sandboxOwnerDomain) {
				target = admins[0]
			} else {
				target = User{ID: bson.NewObjectID(), AppID: appID, Email: input.Email, PasswordHash: passwordHash, Role: "admin", CreatedAt: time.Now().UTC()}
				if _, err := users.InsertOne(ctx, target); err != nil {
					writeError(w, http.StatusInternalServerError, "Could not update owner login")
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"email": input.Email}})
				return
			}
		default:
			writeError(w, http.StatusInternalServerError, "Could not update owner login")
			return
		}

		// credentialsChangedAt also retires bearer tokens issued to the previous
		// owner, which session revocation alone cannot reach.
		if _, err := users.UpdateOne(ctx, bson.M{"_id": target.ID, "appId": appID}, bson.M{"$set": bson.M{"email": input.Email, "password": passwordHash, "credentialsChangedAt": time.Now().UTC()}}); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				writeError(w, http.StatusConflict, "A member of this app already uses that email")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not update owner login")
			return
		}
		// A changed password must end sessions opened with the old one.
		if _, err := app.database.Collection(sessionCollectionName).UpdateMany(ctx,
			bson.M{"userId": target.ID, "revokedAt": bson.M{"$exists": false}},
			bson.M{"$set": bson.M{"revokedAt": time.Now().UTC()}}); err != nil {
			app.logger.Error("could not revoke owner sessions", "request_id", requestIDFromContext(ctx), "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"email": input.Email}})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

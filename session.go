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
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	sessionCollectionName = "_m_stash_sessions"
	accessCookieName      = "m_stash_access"
	refreshCookieName     = "m_stash_refresh"
	csrfCookieName        = "m_stash_csrf"
)

const authenticationSourceContextKey contextKey = "authentication-source"

type authenticationSource string

const (
	bearerAuthentication authenticationSource = "bearer"
	cookieAuthentication authenticationSource = "cookie"
)

type refreshSession struct {
	ID        bson.ObjectID `bson:"_id"`
	FamilyID  bson.ObjectID `bson:"familyId"`
	UserID    bson.ObjectID `bson:"userId"`
	TokenHash string        `bson:"tokenHash"`
	CreatedAt time.Time     `bson:"createdAt"`
	ExpiresAt time.Time     `bson:"expiresAt"`
	RevokedAt *time.Time    `bson:"revokedAt,omitempty"`
	RotatedAt *time.Time    `bson:"rotatedAt,omitempty"`
}

func (app *application) accessTokenTTL() time.Duration {
	if app.config.AccessTokenTTL <= 0 {
		return 15 * time.Minute
	}
	return app.config.AccessTokenTTL
}

func (app *application) refreshSessionTTL() time.Duration {
	if app.config.RefreshSessionTTL <= 0 {
		return 30 * 24 * time.Hour
	}
	return app.config.RefreshSessionTTL
}

func withAuthenticationSource(ctx context.Context, source authenticationSource) context.Context {
	return context.WithValue(ctx, authenticationSourceContextKey, source)
}

func authenticationSourceFromContext(ctx context.Context) authenticationSource {
	source, _ := ctx.Value(authenticationSourceContextKey).(authenticationSource)
	return source
}

func requiresCSRFProtection(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validCSRFToken(r *http.Request) bool {
	csrfCookie, err := r.Cookie(csrfCookieName)
	if err != nil || csrfCookie.Value == "" {
		return false
	}
	headerToken := r.Header.Get("X-CSRF-Token")
	if headerToken == "" || len(headerToken) != len(csrfCookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(headerToken), []byte(csrfCookie.Value)) == 1
}

func browserSessionToken() (string, error) {
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(rawToken), nil
}

func hashBrowserSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (app *application) issueBrowserSession(ctx context.Context, w http.ResponseWriter, user User, familyID bson.ObjectID) error {
	refreshToken, err := browserSessionToken()
	if err != nil {
		return err
	}
	csrfToken, err := browserSessionToken()
	if err != nil {
		return err
	}
	if familyID.IsZero() {
		familyID = bson.NewObjectID()
	}

	now := time.Now().UTC()
	session := refreshSession{
		ID:        bson.NewObjectID(),
		FamilyID:  familyID,
		UserID:    user.ID,
		TokenHash: hashBrowserSessionToken(refreshToken),
		CreatedAt: now,
		ExpiresAt: now.Add(app.refreshSessionTTL()),
	}
	if _, err := app.database.Collection(sessionCollectionName).InsertOne(ctx, session); err != nil {
		return fmt.Errorf("store refresh session: %w", err)
	}

	accessToken, err := app.generateAccessJWT(user.ID.Hex(), user.Email, user.Role)
	if err != nil {
		return fmt.Errorf("generate access token: %w", err)
	}
	app.setBrowserSessionCookies(w, accessToken, refreshToken, csrfToken)
	return nil
}

func (app *application) setBrowserSessionCookies(w http.ResponseWriter, accessToken, refreshToken, csrfToken string) {
	http.SetCookie(w, app.sessionCookie(accessCookieName, accessToken, app.accessTokenTTL(), true))
	http.SetCookie(w, app.sessionCookie(refreshCookieName, refreshToken, app.refreshSessionTTL(), true))
	http.SetCookie(w, app.sessionCookie(csrfCookieName, csrfToken, app.refreshSessionTTL(), false))
}

func (app *application) sessionCookie(name, value string, lifetime time.Duration, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(lifetime.Seconds()),
		Expires:  time.Now().Add(lifetime),
		HttpOnly: httpOnly,
		Secure:   app.config.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (app *application) clearBrowserSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{accessCookieName, refreshCookieName, csrfCookieName} {
		cookie := app.sessionCookie(name, "", -time.Hour, name != csrfCookieName)
		cookie.MaxAge = -1
		http.SetCookie(w, cookie)
	}
}

func (app *application) handleSessionRefresh(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !validCSRFToken(r) {
		writeError(w, http.StatusForbidden, "Missing or invalid CSRF token")
		return
	}

	refreshCookie, err := r.Cookie(refreshCookieName)
	if err != nil || refreshCookie.Value == "" {
		app.clearBrowserSessionCookies(w)
		writeError(w, http.StatusUnauthorized, "Refresh session is missing or invalid")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	sessions := app.database.Collection(sessionCollectionName)
	var session refreshSession
	if err := sessions.FindOne(ctx, bson.M{"tokenHash": hashBrowserSessionToken(refreshCookie.Value)}).Decode(&session); err != nil {
		app.clearBrowserSessionCookies(w)
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusUnauthorized, "Refresh session is missing or invalid")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not refresh browser session")
		return
	}

	now := time.Now().UTC()
	if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		_ = app.revokeSessionFamily(ctx, session.FamilyID, now)
		app.clearBrowserSessionCookies(w)
		writeError(w, http.StatusUnauthorized, "Refresh session is expired or has already been used")
		return
	}

	result, err := sessions.UpdateOne(
		ctx,
		bson.M{"_id": session.ID, "revokedAt": bson.M{"$exists": false}, "expiresAt": bson.M{"$gt": now}},
		bson.M{"$set": bson.M{"revokedAt": now, "rotatedAt": now}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not refresh browser session")
		return
	}
	if result.MatchedCount != 1 {
		_ = app.revokeSessionFamily(ctx, session.FamilyID, now)
		app.clearBrowserSessionCookies(w)
		writeError(w, http.StatusUnauthorized, "Refresh session is expired or has already been used")
		return
	}

	var user User
	if err := app.database.Collection("_users").FindOne(ctx, bson.M{"_id": session.UserID}).Decode(&user); err != nil {
		app.clearBrowserSessionCookies(w)
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusUnauthorized, "User account is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not refresh browser session")
		return
	}
	if err := app.issueBrowserSession(ctx, w, user, session.FamilyID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not refresh browser session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "user": user})
}

func (app *application) handleSessionLogout(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !validCSRFToken(r) {
		writeError(w, http.StatusForbidden, "Missing or invalid CSRF token")
		return
	}

	if refreshCookie, err := r.Cookie(refreshCookieName); err == nil && refreshCookie.Value != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		now := time.Now().UTC()
		_, _ = app.database.Collection(sessionCollectionName).UpdateOne(
			ctx,
			bson.M{"tokenHash": hashBrowserSessionToken(refreshCookie.Value), "revokedAt": bson.M{"$exists": false}},
			bson.M{"$set": bson.M{"revokedAt": now}},
		)
	}
	app.clearBrowserSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (app *application) revokeSessionFamily(ctx context.Context, familyID bson.ObjectID, revokedAt time.Time) error {
	_, err := app.database.Collection(sessionCollectionName).UpdateMany(
		ctx,
		bson.M{"familyId": familyID, "revokedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"revokedAt": revokedAt}},
	)
	return err
}

func ensureSessionIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(sessionCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		{Keys: bson.D{{Key: "tokenHash", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "familyId", Value: 1}, {Key: "revokedAt", Value: 1}}},
	})
	if err != nil {
		return fmt.Errorf("create browser-session indexes: %w", err)
	}
	return nil
}

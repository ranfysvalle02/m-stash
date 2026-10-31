package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/bcrypt"
)

func TestAppKeyFromRequestPrefersHeaderThenQuery(t *testing.T) {
	headerRequest := httptest.NewRequest(http.MethodGet, "/v1/public/ada?app=pk_query", nil)
	headerRequest.Header.Set(appKeyHeader, "pk_header")
	if key := appKeyFromRequest(headerRequest); key != "pk_header" {
		t.Fatalf("header key = %q, want pk_header", key)
	}

	queryRequest := httptest.NewRequest(http.MethodGet, "/v1/public/ada?app=pk_query", nil)
	if key := appKeyFromRequest(queryRequest); key != "pk_query" {
		t.Fatalf("query key = %q, want pk_query", key)
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/v1/public/ada", nil)
	if key := appKeyFromRequest(missingRequest); key != "" {
		t.Fatalf("missing key = %q, want empty", key)
	}
}

func TestResolveAppRejectsMissingKeyButAllowsPreflight(t *testing.T) {
	app := newApplication(Config{}, nil, nil)
	called := false
	handler := app.resolveApp(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	missing := httptest.NewRequest(http.MethodGet, "/v1/me/namespace", nil)
	missingResponse := httptest.NewRecorder()
	handler(missingResponse, missing)
	if called || missingResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing key: called=%t status=%d", called, missingResponse.Code)
	}

	called = false
	preflight := httptest.NewRequest(http.MethodOptions, "/v1/me/namespace", nil)
	preflightResponse := httptest.NewRecorder()
	handler(preflightResponse, preflight)
	if !called || preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight passthrough: called=%t status=%d", called, preflightResponse.Code)
	}
}

func TestMatchesPlatformToken(t *testing.T) {
	app := newApplication(Config{PlatformToken: "platform-token-with-at-least-32-characters"}, nil, nil)
	if !app.matchesPlatformToken("Bearer " + app.config.PlatformToken) {
		t.Fatal("valid platform token rejected")
	}
	if app.matchesPlatformToken("Bearer wrong") {
		t.Fatal("invalid platform token accepted")
	}
	if app.matchesPlatformToken(app.config.PlatformToken) {
		t.Fatal("platform token without Bearer prefix accepted")
	}
}

func TestMatchesAppSecretRequiresResolvedApp(t *testing.T) {
	app := newApplication(Config{}, nil, nil)
	secretKey := secretKeyPrefix + "example-secret-value"
	hash, err := bcrypt.GenerateFromPassword([]byte(secretKey), bcryptCost)
	if err != nil {
		t.Fatalf("hash secret: %v", err)
	}
	record := &AppRecord{ID: bson.NewObjectID(), SecretKeyHash: string(hash)}

	withoutApp := httptest.NewRequest(http.MethodPost, "/v1/shared/resources/score", nil)
	if app.matchesAppSecret(withoutApp.Context(), "Bearer "+secretKey) {
		t.Fatal("secret matched without a resolved app")
	}

	withAppRequest := withoutApp.WithContext(withApp(withoutApp.Context(), record))
	if !app.matchesAppSecret(withAppRequest.Context(), "Bearer "+secretKey) {
		t.Fatal("valid app secret rejected")
	}
	if app.matchesAppSecret(withAppRequest.Context(), "Bearer sk_wrong") {
		t.Fatal("wrong app secret accepted")
	}
	if app.matchesAppSecret(withAppRequest.Context(), "Bearer "+secretKey[len(secretKeyPrefix):]) {
		t.Fatal("secret without sk_ prefix accepted")
	}
}

func TestProvisionAppInputValidation(t *testing.T) {
	valid := provisionAppInput{Slug: " My-App ", DisplayName: "  "}
	if err := valid.normalizeAndValidate(); err != nil {
		t.Fatalf("valid provision input rejected: %v", err)
	}
	if valid.Slug != "my-app" || valid.DisplayName != "my-app" {
		t.Fatalf("normalized input = %#v", valid)
	}

	partialAdmin := provisionAppInput{Slug: "my-app", AdminEmail: "admin@example.com"}
	if err := partialAdmin.normalizeAndValidate(); err == nil {
		t.Fatal("admin email without password was accepted")
	}

	badSlug := provisionAppInput{Slug: "a"}
	if err := badSlug.normalizeAndValidate(); err == nil {
		t.Fatal("invalid slug was accepted")
	}
}

func TestAuthenticateRejectsTokenFromAnotherApp(t *testing.T) {
	app := newApplication(Config{
		JWTSecret:   "test-secret-with-at-least-32-characters",
		JWTIssuer:   "test-issuer",
		JWTAudience: "test-audience",
	}, nil, nil)
	tokenApp := bson.NewObjectID()
	requestApp := &AppRecord{ID: bson.NewObjectID()}
	token, err := app.generateJWT("507f1f77bcf86cd799439011", "ada@example.com", "user", tokenApp.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/me/namespace", nil)
	request = request.WithContext(withApp(request.Context(), requestApp))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, _ *http.Request, _ *Claims) {
		w.WriteHeader(http.StatusNoContent)
	})(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-app token status = %d, want %d", response.Code, http.StatusForbidden)
	}

	matching := httptest.NewRequest(http.MethodGet, "/v1/me/namespace", nil)
	matching = matching.WithContext(withApp(matching.Context(), &AppRecord{ID: tokenApp}))
	matching.Header.Set("Authorization", "Bearer "+token)
	matchingResponse := httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, _ *http.Request, _ *Claims) {
		w.WriteHeader(http.StatusNoContent)
	})(matchingResponse, matching)
	if matchingResponse.Code != http.StatusNoContent {
		t.Fatalf("matching-app token status = %d, want %d", matchingResponse.Code, http.StatusNoContent)
	}
}

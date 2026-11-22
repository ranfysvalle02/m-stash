package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/bcrypt"
)

func oauthTestApp() *application {
	return newApplication(Config{
		JWTSecret:      "test-secret-value-that-is-at-least-32-bytes-long",
		JWTIssuer:      "m-stash",
		JWTAudience:    "m-stash",
		PublicBaseURL:  "https://m-stash.example",
		AllowedOrigins: []string{"https://app.example"},
	}, nil, nil)
}

// --- redirect_uri allowlisting --------------------------------------------

func TestValidateOAuthRedirect(t *testing.T) {
	app := oauthTestApp()
	request := httptest.NewRequest(http.MethodGet, "https://m-stash.example/v1/auth/oauth/github/start", nil)

	liveRecord := &AppRecord{AllowedOrigins: []string{"https://tenant.example"}}
	sandboxRecord := &AppRecord{Sandbox: true, AllowedOrigins: []string{"*"}}

	cases := []struct {
		name     string
		record   *AppRecord
		input    string
		wantErr  bool
		wantSame string // expected exact return value; empty means "echo input"
	}{
		{name: "empty defaults to login page", record: liveRecord, input: "", wantSame: "https://m-stash.example/login"},
		{name: "same deployment origin", record: liveRecord, input: "https://m-stash.example/login?next=1"},
		{name: "explicit record origin", record: liveRecord, input: "https://tenant.example/callback"},
		{name: "deployment allowed origin", record: liveRecord, input: "https://app.example/callback"},
		{name: "external host rejected", record: liveRecord, input: "https://evil.example/callback", wantErr: true},
		{name: "localhost rejected for live non-wildcard", record: liveRecord, input: "http://localhost:5173/login", wantErr: true},
		{name: "localhost allowed for sandbox", record: sandboxRecord, input: "http://localhost:5173/login"},
		{name: "wildcard still rejects external host", record: sandboxRecord, input: "https://evil.example/login", wantErr: true},
		{name: "garbage rejected", record: liveRecord, input: "::not a url::", wantErr: true},
		{name: "scheme-relative rejected", record: liveRecord, input: "//evil.example/login", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := app.validateOAuthRedirect(request, testCase.record, testCase.input)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", testCase.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", testCase.input, err)
			}
			want := testCase.wantSame
			if want == "" {
				want = testCase.input
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// --- state JWT + cookie nonce ---------------------------------------------

func TestOAuthStateSignAndVerify(t *testing.T) {
	app := oauthTestApp()
	now := time.Now()
	state := oauthStateClaims{
		Nonce:       "nonce-value",
		AppID:       bson.NewObjectID().Hex(),
		Provider:    "github",
		RedirectURI: "https://m-stash.example/login",
		Verifier:    "verifier-value",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    app.config.JWTIssuer,
			Audience:  jwt.ClaimStrings{oauthStateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(oauthStateTTL)),
		},
	}
	signed, err := app.signOAuthState(state)
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}
	parsed, err := app.parseOAuthState(signed)
	if err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if parsed.Nonce != state.Nonce || parsed.Provider != "github" || parsed.Verifier != "verifier-value" || parsed.RedirectURI != state.RedirectURI {
		t.Fatalf("round-tripped claims mismatch: %+v", parsed)
	}

	// Tampering with the token invalidates the signature.
	tampered := signed[:len(signed)-2] + "xy"
	if _, err := app.parseOAuthState(tampered); err == nil {
		t.Fatal("expected tampered state to be rejected")
	}

	// A token with the deployment audience (a real session JWT) is not a state.
	sessionToken, err := app.generateJWT("507f1f77bcf86cd799439011", "u@example.com", "user", bson.NewObjectID().Hex())
	if err != nil {
		t.Fatalf("generate session jwt: %v", err)
	}
	if _, err := app.parseOAuthState(sessionToken); err == nil {
		t.Fatal("expected a session JWT to be rejected as oauth state")
	}

	// An expired state is rejected.
	expired := state
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))
	expiredSigned, err := app.signOAuthState(expired)
	if err != nil {
		t.Fatalf("sign expired state: %v", err)
	}
	if _, err := app.parseOAuthState(expiredSigned); err == nil {
		t.Fatal("expected expired state to be rejected")
	}
}

func TestAuthenticateRejectsOAuthStateJWT(t *testing.T) {
	app := oauthTestApp()
	state := oauthStateClaims{
		Nonce:    "nonce",
		AppID:    bson.NewObjectID().Hex(),
		Provider: "github",
		Verifier: "verifier",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    app.config.JWTIssuer,
			Subject:   "507f1f77bcf86cd799439011",
			Audience:  jwt.ClaimStrings{oauthStateAudience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(oauthStateTTL)),
		},
	}
	signed, err := app.signOAuthState(state)
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}

	called := false
	handler := app.authenticate(func(http.ResponseWriter, *http.Request, *Claims) { called = true })
	request := httptest.NewRequest(http.MethodGet, "/v1/me/namespace", nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if called {
		t.Fatal("authenticate accepted an oauth state JWT")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

// --- PKCE ------------------------------------------------------------------

func TestCodeChallengeS256MatchesRFC7636(t *testing.T) {
	// RFC 7636 Appendix B worked example.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := codeChallengeS256(verifier); got != want {
		t.Fatalf("codeChallengeS256 = %q, want %q", got, want)
	}
}

// --- GitHub adapter --------------------------------------------------------

func TestGitHubAdapterExchange(t *testing.T) {
	var gotVerifier, gotSecret string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			gotVerifier = r.Form.Get("code_verifier")
			gotSecret = r.Form.Get("client_secret")
			writeTestJSON(w, map[string]any{"access_token": "gho_test", "token_type": "bearer"})
		case "/user":
			writeTestJSON(w, map[string]any{"id": 4242, "login": "octocat", "name": "The Octocat", "avatar_url": "https://avatars.example/o.png"})
		case "/emails":
			writeTestJSON(w, []map[string]any{
				{"email": "secondary@example.com", "primary": false, "verified": true},
				{"email": "primary@example.com", "primary": true, "verified": true},
				{"email": "unverified@example.com", "primary": false, "verified": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &githubOAuthAdapter{
		clientID:       "client-id",
		clientSecret:   "client-secret",
		httpClient:     oauthHTTPClient(),
		tokenEndpoint:  server.URL + "/token",
		userEndpoint:   server.URL + "/user",
		emailsEndpoint: server.URL + "/emails",
	}
	identity, err := adapter.Exchange(context.Background(), "https://m-stash.example/cb", "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if gotVerifier != "the-verifier" {
		t.Fatalf("token endpoint got code_verifier %q, want PKCE verifier", gotVerifier)
	}
	if gotSecret != "client-secret" {
		t.Fatalf("token endpoint got client_secret %q", gotSecret)
	}
	if identity.Subject != "4242" {
		t.Fatalf("subject = %q, want 4242", identity.Subject)
	}
	if identity.Email != "primary@example.com" || !identity.EmailVerified {
		t.Fatalf("email = %q verified=%t, want primary verified", identity.Email, identity.EmailVerified)
	}
	if identity.UsernameHint != "octocat" {
		t.Fatalf("username hint = %q", identity.UsernameHint)
	}
}

func TestGitHubAdapterRejectsUnverifiedEmails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			writeTestJSON(w, map[string]any{"access_token": "gho_test"})
		case "/user":
			writeTestJSON(w, map[string]any{"id": 7, "login": "ghost"})
		case "/emails":
			writeTestJSON(w, []map[string]any{{"email": "ghost@example.com", "primary": true, "verified": false}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &githubOAuthAdapter{
		clientID:       "id",
		clientSecret:   "secret",
		httpClient:     oauthHTTPClient(),
		tokenEndpoint:  server.URL + "/token",
		userEndpoint:   server.URL + "/user",
		emailsEndpoint: server.URL + "/emails",
	}
	identity, err := adapter.Exchange(context.Background(), "https://m-stash.example/cb", "code", "verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if identity.EmailVerified || identity.Email != "" {
		t.Fatalf("unverified email must not be trusted: email=%q verified=%t", identity.Email, identity.EmailVerified)
	}
}

// --- Google adapter --------------------------------------------------------

func TestGoogleAdapterExchange(t *testing.T) {
	var gotVerifier string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			gotVerifier = r.Form.Get("code_verifier")
			writeTestJSON(w, map[string]any{"access_token": "ya29.test"})
		case "/userinfo":
			writeTestJSON(w, map[string]any{"sub": "google-sub-1", "email": "jane@example.com", "email_verified": true, "name": "Jane Doe", "picture": "https://pic.example/j.png"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &googleOAuthAdapter{
		clientID:         "id",
		clientSecret:     "secret",
		httpClient:       oauthHTTPClient(),
		tokenEndpoint:    server.URL + "/token",
		userInfoEndpoint: server.URL + "/userinfo",
	}
	identity, err := adapter.Exchange(context.Background(), "https://m-stash.example/cb", "code", "pkce-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if gotVerifier != "pkce-verifier" {
		t.Fatalf("token endpoint got code_verifier %q", gotVerifier)
	}
	if identity.Subject != "google-sub-1" || identity.Email != "jane@example.com" || !identity.EmailVerified {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if identity.Provider != "google" {
		t.Fatalf("provider = %q", identity.Provider)
	}
}

func TestGoogleAdapterHonorsUnverifiedEmailFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			writeTestJSON(w, map[string]any{"access_token": "ya29.test"})
		case "/userinfo":
			writeTestJSON(w, map[string]any{"sub": "sub-2", "email": "x@example.com", "email_verified": false, "name": "X"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &googleOAuthAdapter{
		clientID:         "id",
		clientSecret:     "secret",
		httpClient:       oauthHTTPClient(),
		tokenEndpoint:    server.URL + "/token",
		userInfoEndpoint: server.URL + "/userinfo",
	}
	identity, err := adapter.Exchange(context.Background(), "https://m-stash.example/cb", "code", "verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if identity.EmailVerified {
		t.Fatal("google adapter trusted an unverified email")
	}
}

// --- slug derivation -------------------------------------------------------

func TestBaseSlugFromHint(t *testing.T) {
	cases := []struct {
		hint  string
		email string
		want  string
	}{
		{"Octocat", "octo@example.com", "octocat"},
		{"", "jane.doe@example.com", "jane-doe"},
		{"ab", "a@b.com", "user"},            // hint too short; email local "a" too short
		{"admin", "owner@example.com", "owner"}, // reserved hint falls back to email local-part
		{"José", "jose@example.com", "jos"},   // non-ascii stripped
		{"  Multiple   Spaces  ", "x@y.com", "multiple-spaces"},
	}
	for _, testCase := range cases {
		if got := baseSlugFromHint(testCase.hint, testCase.email); got != testCase.want {
			t.Errorf("baseSlugFromHint(%q, %q) = %q, want %q", testCase.hint, testCase.email, got, testCase.want)
		}
	}
}

func TestSlugWithSuffixStaysValid(t *testing.T) {
	for _, base := range []string{"octocat", "admin", "", strings.Repeat("abcdefghij", 4)} {
		slug := slugWithSuffix(base)
		if err := validateNamespaceSlug(slug); err != nil {
			t.Errorf("slugWithSuffix(%q) = %q is invalid: %v", base, slug, err)
		}
		if !strings.Contains(slug, "-") {
			t.Errorf("slugWithSuffix(%q) = %q has no suffix separator", base, slug)
		}
	}
}

// --- adapter registry ------------------------------------------------------

func TestNewOAuthAdaptersRespectsConfiguredPairs(t *testing.T) {
	none := newOAuthAdapters(Config{})
	if len(none) != 0 {
		t.Fatalf("expected no adapters, got %d", len(none))
	}
	githubOnly := newOAuthAdapters(Config{GitHubClientID: "id", GitHubClientSecret: "secret"})
	if len(githubOnly) != 1 {
		t.Fatalf("expected 1 adapter, got %d", len(githubOnly))
	}
	if _, ok := githubOnly["github"]; !ok {
		t.Fatal("github adapter missing")
	}
	both := newOAuthAdapters(Config{GitHubClientID: "a", GitHubClientSecret: "b", GoogleClientID: "c", GoogleClientSecret: "d"})
	app := newApplication(Config{GitHubClientID: "a", GitHubClientSecret: "b", GoogleClientID: "c", GoogleClientSecret: "d"}, nil, nil)
	names := app.oauthProviderNames()
	if len(both) != 2 || len(names) != 2 || names[0] != "github" || names[1] != "google" {
		t.Fatalf("expected sorted [github google], got %v (adapters=%d)", names, len(both))
	}
}

// --- config pair validation ------------------------------------------------

func TestValidateOAuthPair(t *testing.T) {
	if err := validateOAuthPair("GITHUB", "", ""); err != nil {
		t.Fatalf("both empty should pass: %v", err)
	}
	if err := validateOAuthPair("GITHUB", "id", "secret"); err != nil {
		t.Fatalf("both set should pass: %v", err)
	}
	if err := validateOAuthPair("GITHUB", "id", ""); err == nil {
		t.Fatal("id without secret should fail")
	}
	if err := validateOAuthPair("GOOGLE", "", "secret"); err == nil {
		t.Fatal("secret without id should fail")
	}
}

// --- password-less user safety ---------------------------------------------

func TestPasswordLessUserCannotAuthenticate(t *testing.T) {
	// The dummy hash used to equalize login timing must be a real bcrypt hash.
	cost, err := bcrypt.Cost(dummyPasswordHash)
	if err != nil {
		t.Fatalf("dummy hash is not a valid bcrypt hash: %v", err)
	}
	if cost != bcryptCost {
		t.Fatalf("dummy hash cost = %d, want %d", cost, bcryptCost)
	}
	// Comparing any password against it fails (it is never a user's real hash).
	if err := bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte("anything")); err == nil {
		t.Fatal("dummy hash unexpectedly matched a password")
	}
	// An empty stored hash (an OAuth-only account) can never satisfy a password.
	if err := bcrypt.CompareHashAndPassword([]byte(""), []byte("anything")); err == nil {
		t.Fatal("empty password hash must never validate")
	}
}

// --- ticket hashing --------------------------------------------------------

func TestOAuthTicketHashIsStable(t *testing.T) {
	token, err := newOAuthTicketToken()
	if err != nil {
		t.Fatalf("new ticket: %v", err)
	}
	if !strings.HasPrefix(token, oauthTicketPrefix) {
		t.Fatalf("ticket %q missing prefix", token)
	}
	if hashOAuthTicket(token) != hashOAuthTicket(token) {
		t.Fatal("ticket hash is not stable")
	}
	if hashOAuthTicket(token) == token {
		t.Fatal("ticket hash must not equal the raw token")
	}
}

func writeTestJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

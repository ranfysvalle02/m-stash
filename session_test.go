package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCookieAuthenticatedWriteRequiresCSRFToken(t *testing.T) {
	app := newApplication(Config{
		JWTSecret:   "test-secret-with-at-least-32-characters",
		JWTIssuer:   "test-issuer",
		JWTAudience: "test-audience",
	}, nil, nil)
	accessToken, err := app.generateAccessJWT("507f1f77bcf86cd799439011", "ada@example.com", "user")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/me/resources/project", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: accessToken})
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token"})
	response := httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		w.WriteHeader(http.StatusNoContent)
	})(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("write without CSRF token status = %d, want %d", response.Code, http.StatusForbidden)
	}

	request.Header.Set("X-CSRF-Token", "csrf-token")
	response = httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		if authenticationSourceFromContext(r.Context()) != cookieAuthentication {
			t.Fatal("expected cookie authentication source")
		}
		w.WriteHeader(http.StatusNoContent)
	})(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("write with CSRF token status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestBrowserSessionCookiesHaveExpectedSecurityAttributes(t *testing.T) {
	app := newApplication(Config{SessionCookieSecure: true}, nil, nil)
	response := httptest.NewRecorder()
	app.setBrowserSessionCookies(response, "access", "refresh", "csrf")

	cookies := response.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("cookie count = %d, want 3", len(cookies))
	}
	for _, cookie := range cookies {
		if !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
			t.Fatalf("cookie %q security attributes = %#v", cookie.Name, cookie)
		}
		if cookie.Name == csrfCookieName {
			if cookie.HttpOnly {
				t.Fatal("CSRF cookie must be readable by the same-origin client")
			}
			continue
		}
		if !cookie.HttpOnly {
			t.Fatalf("cookie %q must be HttpOnly", cookie.Name)
		}
	}
}

func TestBrowserSessionTokensAreOpaqueAndHashed(t *testing.T) {
	first, err := browserSessionToken()
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	second, err := browserSessionToken()
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if first == second || len(first) < 40 {
		t.Fatalf("opaque tokens should be unique and sufficiently long: %q, %q", first, second)
	}
	if hashBrowserSessionToken(first) == first || len(hashBrowserSessionToken(first)) != 64 {
		t.Fatal("browser session token was not represented by a SHA-256 hash")
	}
}

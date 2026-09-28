package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersAreAppliedToApplicationResponses(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if contentSecurityPolicy := response.Header().Get("Content-Security-Policy"); !strings.Contains(contentSecurityPolicy, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q", contentSecurityPolicy)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options was not set")
	}
	if response.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be set for an unverified HTTP request")
	}
}

func TestSecurityHeadersTrustConfiguredForwardedHTTPS(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}, TrustProxy: true}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS was not set for trusted forwarded HTTPS")
	}
}

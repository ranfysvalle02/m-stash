package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSecurityHeadersAreAppliedToApplicationResponses(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if contentSecurityPolicy := response.Header().Get("Content-Security-Policy"); !strings.Contains(contentSecurityPolicy, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q", contentSecurityPolicy)
	}
	if contentSecurityPolicy := response.Header().Get("Content-Security-Policy"); !strings.Contains(contentSecurityPolicy, "frame-src 'self'") {
		t.Fatalf("Content-Security-Policy missing explicit frame-src: %q", contentSecurityPolicy)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options was not set")
	}
	if response.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be set for an unverified HTTP request")
	}
}

func TestAppCORSPreflightAndActualOrigin(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"https://platform.example"}}, nil, nil)
	app.appCache.put("pk_test", &AppRecord{ID: bson.NewObjectID(), AllowedOrigins: []string{"http://localhost:8000"}})
	handler := app.Handler(nil)

	for _, tc := range []struct {
		name, method, path, origin, headers string
		key                                 bool
		wantStatus                          int
		wantOrigin                          string
	}{
		{"keyless preflight", "OPTIONS", "/v1/auth/verify", "http://localhost:8000", "authorization, content-type, x-m-stash-app", false, 200, "http://localhost:8000"},
		{"preflight is not authorization", "OPTIONS", "/v1/auth/verify", "https://evil.example", "x-m-stash-app", false, 200, "https://evil.example"},
		{"actual allowed", "GET", "/v1/auth/verify", "http://localhost:8000", "", true, 401, "http://localhost:8000"},
		{"actual denied", "GET", "/v1/auth/verify", "https://evil.example", "", true, 403, ""},
		{"mutation denied", "POST", "/v1/auth/signup", "https://evil.example", "", true, 403, ""},
		{"keyed preflight denied", "OPTIONS", "/v1/auth/verify?app=pk_test", "https://evil.example", "x-m-stash-app", false, 403, ""},
		{"platform preflight still restricted", "OPTIONS", "/v1/builder/me", "https://evil.example", "x-m-stash-app", false, 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Origin", tc.origin)
			if tc.key {
				req.Header.Set(appKeyHeader, "pk_test")
			}
			if tc.method == "OPTIONS" {
				req.Header.Set("Access-Control-Request-Method", "GET")
				req.Header.Set("Access-Control-Request-Headers", tc.headers)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.wantStatus || response.Header().Get("Access-Control-Allow-Origin") != tc.wantOrigin {
				t.Fatalf("status = %d, origin = %q; want %d, %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"), tc.wantStatus, tc.wantOrigin)
			}
		})
	}
}

func TestVerifyIncludesAppAndRejectsOtherApp(t *testing.T) {
	app := newApplication(Config{JWTSecret: "secret", JWTIssuer: "issuer", JWTAudience: "audience", AllowedOrigins: []string{"*"}}, nil, nil)
	id, otherID := bson.NewObjectID(), bson.NewObjectID()
	app.appCache.put("pk_one", &AppRecord{ID: id})
	app.appCache.put("pk_two", &AppRecord{ID: otherID})
	token, err := app.generateJWT("user-id", "user@example.com", "user", id.Hex())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key    string
		status int
	}{{"pk_one", 200}, {"pk_two", 403}} {
		req := httptest.NewRequest("GET", "/v1/auth/verify", nil)
		req.Header.Set(appKeyHeader, tc.key)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		app.Handler(nil).ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("%s: status = %d, want %d", tc.key, response.Code, tc.status)
		}
		if tc.status == 200 {
			var body struct {
				Claims struct {
					AppID string `json:"appId"`
					UID   string `json:"uid"`
				} `json:"claims"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Claims.AppID != id.Hex() || body.Claims.UID != "user-id" {
				t.Fatalf("unexpected claims: %s (%v)", response.Body.String(), err)
			}
		}
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

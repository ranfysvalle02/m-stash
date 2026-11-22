package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// adminRoutesTestApp wires a real Handler (full middleware stack + mux) over a
// nil database, so routing and the operator guard can be exercised end-to-end
// without a live MongoDB. Handlers that touch the database are only reached once
// auth has passed, so these tests deliberately stop at the guard.
func adminRoutesTestApp(adminEmail string) *application {
	return newApplication(Config{
		JWTSecret:      "0123456789abcdef0123456789abcdef",
		JWTIssuer:      "m-stash",
		JWTAudience:    "m-stash",
		AllowedOrigins: []string{"*"},
		AdminEmail:     adminEmail,
	}, nil, nil)
}

func TestAdminRouteRejectsAnonymous(t *testing.T) {
	handler := adminRoutesTestApp("").Handler(&authRateLimiter{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /v1/admin/overview = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAdminRouteRejectsBuilder(t *testing.T) {
	app := adminRoutesTestApp("")
	handler := app.Handler(&authRateLimiter{})
	token, err := app.generateJWT(bson.NewObjectID().Hex(), "builder@example.com", builderRole, bson.NilObjectID.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	for _, path := range []string{"/v1/admin/overview", "/v1/admin/settings", "/v1/admin/apps", "/v1/admin/builders", "/v1/admin/outbox"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("builder %s = %d, want %d", path, rec.Code, http.StatusForbidden)
		}
	}
}

func TestAdminPreflightSucceeds(t *testing.T) {
	handler := adminRoutesTestApp("").Handler(&authRateLimiter{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/admin/settings", nil)
	req.Header.Set("Origin", "https://example.com")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin preflight = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestManifestAdvertisesOperatorBootstrap verifies the service manifest reports
// that an operator is configured — without ever exposing the admin email.
func TestManifestAdvertisesOperatorBootstrap(t *testing.T) {
	const adminEmail = "secret-admin@example.com"
	handler := adminRoutesTestApp(adminEmail).Handler(&authRateLimiter{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/m-stash.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", rec.Code)
	}
	var manifest struct {
		Capabilities struct {
			OperatorBootstrap struct {
				Configured bool   `json:"configured"`
				Endpoint   string `json:"endpoint"`
			} `json:"operatorBootstrap"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !manifest.Capabilities.OperatorBootstrap.Configured {
		t.Fatal("manifest should report operatorBootstrap.configured = true")
	}
	if manifest.Capabilities.OperatorBootstrap.Endpoint != "/v1/admin" {
		t.Fatalf("operator endpoint = %q, want /v1/admin", manifest.Capabilities.OperatorBootstrap.Endpoint)
	}
	if strings.Contains(rec.Body.String(), adminEmail) {
		t.Fatal("the manifest must never expose the admin email")
	}
}

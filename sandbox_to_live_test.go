package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEffectiveLimitsFallsBackForLegacySandbox(t *testing.T) {
	// A sandbox document created before per-app limits existed has no stored
	// Limits, so it must fall back to the sandbox quotas without a migration.
	legacy := &AppRecord{Sandbox: true}
	got := effectiveLimits(legacy)
	want := sandboxLimits()
	if got != want {
		t.Fatalf("legacy sandbox limits = %#v, want %#v", got, want)
	}

	// A non-sandbox app with no stored limits is unbounded (all zero).
	unbounded := effectiveLimits(&AppRecord{})
	if unbounded != (AppLimits{}) {
		t.Fatalf("operator app limits = %#v, want zero value", unbounded)
	}

	// Stored limits always win, for both sandbox and live apps.
	stored := AppLimits{MaxUsers: 7, MaxRecordsPerNamespace: 8, MaxRecordBytes: 9}
	if got := effectiveLimits(&AppRecord{Sandbox: true, Limits: &stored}); got != stored {
		t.Fatalf("stored sandbox limits = %#v, want %#v", got, stored)
	}
	if got := effectiveLimits(&AppRecord{Limits: &stored}); got != stored {
		t.Fatalf("stored live limits = %#v, want %#v", got, stored)
	}
}

func TestFreeTierLimitsFromConfig(t *testing.T) {
	cfg := Config{FreeTierMaxUsers: 10, FreeTierMaxRecords: 20, FreeTierMaxRecordBytes: 30}
	got := freeTierLimits(cfg)
	want := AppLimits{MaxUsers: 10, MaxRecordsPerNamespace: 20, MaxRecordBytes: 30}
	if got != want {
		t.Fatalf("freeTierLimits = %#v, want %#v", got, want)
	}
}

func TestClaimTokenHashAndVerify(t *testing.T) {
	token, err := randomHex(24)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	hash := hashClaimToken(token)
	if hash == token {
		t.Fatal("hash must not equal the plaintext token")
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 (sha-256 hex)", len(hash))
	}
	if !verifyClaimToken(token, hash) {
		t.Fatal("the matching token failed verification")
	}
	if verifyClaimToken(token+"x", hash) {
		t.Fatal("a wrong token passed verification")
	}
	if verifyClaimToken("", hash) || verifyClaimToken(token, "") {
		t.Fatal("empty token or hash must never verify")
	}
}

func TestNormalizeOrigins(t *testing.T) {
	origins, err := normalizeOrigins([]string{" https://a.example.com ", "", "https://b.example.com"})
	if err != nil {
		t.Fatalf("normalizeOrigins: %v", err)
	}
	if len(origins) != 2 || origins[0] != "https://a.example.com" || origins[1] != "https://b.example.com" {
		t.Fatalf("origins = %#v", origins)
	}
	if _, err := normalizeOrigins([]string{"has space"}); err == nil {
		t.Fatal("an origin with whitespace must be rejected")
	}
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "https://x.example.com"
	}
	if _, err := normalizeOrigins(tooMany); err == nil {
		t.Fatal("more than 20 origins must be rejected")
	}
}

func TestTrySandboxDisabledReturns404(t *testing.T) {
	app := newApplication(Config{TrySandbox: false}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/try", nil)
	rec := httptest.NewRecorder()
	app.handleTry(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled /v1/try status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBuilderSignupDisabledReturns404(t *testing.T) {
	app := newApplication(Config{BuilderSignupOpen: false}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/builder/auth/signup", strings.NewReader(`{"email":"a@b.com","password":"a-long-password"}`))
	rec := httptest.NewRecorder()
	app.handleBuilderSignup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled signup status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBuilderAppRoutesClaimSegmentBeforeObjectIDParse(t *testing.T) {
	app := testBuilderApp()
	claims := &Claims{UID: bson.NewObjectID().Hex(), Role: builderRole}
	// A GET to the claim path must reach claimBuilderApp (which rejects non-POST
	// with 405), not the ObjectID parser (which would 400 on "claim").
	req := httptest.NewRequest(http.MethodGet, "/v1/builder/apps/claim", nil)
	rec := httptest.NewRecorder()
	app.handleBuilderApp(rec, req, claims)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("claim routing status = %d, want %d (405 proves it reached claimBuilderApp)", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestClaimRequiresKeyAndToken(t *testing.T) {
	app := testBuilderApp()
	claims := &Claims{UID: bson.NewObjectID().Hex(), Role: builderRole}
	req := httptest.NewRequest(http.MethodPost, "/v1/builder/apps/claim", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	app.handleBuilderApp(rec, req, claims)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty claim body status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestServiceManifestAdvertisesNewCapabilities(t *testing.T) {
	app := newApplication(Config{TrySandbox: true, BuilderSignupOpen: true}, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/m-stash.json", nil)
	rec := httptest.NewRecorder()
	app.handleServiceManifest(rec, req)

	var manifest map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	capabilities := manifest["capabilities"].(map[string]any)
	trySandbox := capabilities["trySandbox"].(map[string]any)
	if trySandbox["enabled"] != true || trySandbox["endpoint"] != "/v1/try" {
		t.Fatalf("trySandbox capability = %#v", trySandbox)
	}
	builderSignup := capabilities["builderSignup"].(map[string]any)
	if builderSignup["enabled"] != true || builderSignup["endpoint"] != "/v1/builder/auth/signup" {
		t.Fatalf("builderSignup capability = %#v", builderSignup)
	}

	// With the flags off, enabled must be false so the UI hides the features.
	off := newApplication(Config{}, nil, nil)
	offRec := httptest.NewRecorder()
	off.handleServiceManifest(offRec, httptest.NewRequest(http.MethodGet, "/.well-known/m-stash.json", nil))
	var offManifest map[string]any
	if err := json.NewDecoder(offRec.Body).Decode(&offManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	offCaps := offManifest["capabilities"].(map[string]any)
	if offCaps["trySandbox"].(map[string]any)["enabled"] != false {
		t.Fatal("trySandbox must report disabled when the flag is off")
	}
	if offCaps["builderSignup"].(map[string]any)["enabled"] != false {
		t.Fatal("builderSignup must report disabled when the flag is off")
	}
}

func TestVibeStarterReadmeClaimedVariant(t *testing.T) {
	record := &vibeShareRecord{
		Idea:           "a notes app",
		Spec:           vibeSpec{Name: "Notes", Sections: []vibeSection{}},
		PublishableKey: "pk_example",
		ExpiresAt:      time.Now().Add(24 * time.Hour).UTC(),
	}

	unclaimed := vibeStarterReadme(record, "https://m-stash.example", false)
	if !strings.Contains(unclaimed, "Keep it past the sandbox") {
		t.Fatal("unclaimed readme must explain how to keep the sandbox")
	}
	if !strings.Contains(unclaimed, "claim") {
		t.Fatal("unclaimed readme should mention claiming")
	}

	claimed := vibeStarterReadme(record, "https://m-stash.example", true)
	if strings.Contains(claimed, "Keep it past the sandbox") {
		t.Fatal("claimed readme must drop the sandbox-expiry section")
	}
	if !strings.Contains(claimed, "It's yours now") {
		t.Fatal("claimed readme must state the app is kept")
	}
}

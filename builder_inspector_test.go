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

// handleBuilderApp parses and routes the path before any database access, so the
// validation branches below are exercised without a live Mongo. These guard the
// owner-scoped inspector routes added in v1.34.0.

func dispatchBuilderApp(t *testing.T, method, path, ownerUID string) *httptest.ResponseRecorder {
	t.Helper()
	app := testBuilderApp()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	app.handleBuilderApp(rec, req, &Claims{UID: ownerUID})
	return rec
}

func TestHandleBuilderAppRejectsInvalidOwnerIdentity(t *testing.T) {
	appID := bson.NewObjectID().Hex()
	rec := dispatchBuilderApp(t, http.MethodGet, "/v1/builder/apps/"+appID+"/users", "not-a-hex-id")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandleBuilderAppRejectsInvalidAppID(t *testing.T) {
	owner := bson.NewObjectID().Hex()
	rec := dispatchBuilderApp(t, http.MethodGet, "/v1/builder/apps/not-a-hex/users", owner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Invalid application id") {
		t.Fatalf("body = %q, want it to mention an invalid application id", rec.Body.String())
	}
}

func TestHandleBuilderAppRejectsInvalidUserID(t *testing.T) {
	owner := bson.NewObjectID().Hex()
	appID := bson.NewObjectID().Hex()
	rec := dispatchBuilderApp(t, http.MethodDelete, "/v1/builder/apps/"+appID+"/users/not-a-hex", owner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Invalid user id") {
		t.Fatalf("body = %q, want it to mention an invalid user id", rec.Body.String())
	}
}

func TestHandleBuilderAppRejectsInvalidResourceID(t *testing.T) {
	owner := bson.NewObjectID().Hex()
	appID := bson.NewObjectID().Hex()
	rec := dispatchBuilderApp(t, http.MethodDelete, "/v1/builder/apps/"+appID+"/resources/not-a-hex", owner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Invalid record id") {
		t.Fatalf("body = %q, want it to mention an invalid record id", rec.Body.String())
	}
}

func TestHandleBuilderAppUnknownRouteNotFound(t *testing.T) {
	owner := bson.NewObjectID().Hex()
	appID := bson.NewObjectID().Hex()
	rec := dispatchBuilderApp(t, http.MethodGet, "/v1/builder/apps/"+appID+"/bogus", owner)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// The inspector's safe projection must never carry a password, and an empty
// username must be omitted so the console can treat it as "no handle".
func TestInspectedUserOmitsSecrets(t *testing.T) {
	payload, err := json.Marshal(inspectedUser{
		ID:        bson.NewObjectID().Hex(),
		Email:     "user@example.com",
		Role:      "user",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := strings.ToLower(string(payload))
	if strings.Contains(body, "password") {
		t.Fatalf("inspected user must never serialize a password: %s", payload)
	}
	if strings.Contains(string(payload), "username") {
		t.Fatalf("empty username must be omitted: %s", payload)
	}
}

// deleteBuilderApp and the inspector return AppRecord-derived data; the owner id
// and secret hash must never reach the wire, and sandbox must stay omitted on
// live apps so a claimed app never looks ephemeral.
func TestAppRecordNeverSerializesOwnerOrSecret(t *testing.T) {
	payload, err := json.Marshal(AppRecord{
		ID:             bson.NewObjectID(),
		OwnerID:        bson.NewObjectID(),
		Slug:           "demo",
		DisplayName:    "Demo",
		PublishableKey: "pk_demo",
		SecretKeyHash:  "super-secret-hash",
		Active:         true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"ownerId", "secretKeyHash", "super-secret-hash", "claimTokenHash", "\"sandbox\""} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("AppRecord JSON leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), "pk_demo") {
		t.Fatalf("AppRecord JSON should carry the publishable key: %s", payload)
	}
}

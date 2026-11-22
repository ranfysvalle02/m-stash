package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func testBuilderApp() *application {
	return &application{config: Config{
		JWTSecret:   "0123456789abcdef0123456789abcdef",
		JWTIssuer:   "m-stash",
		JWTAudience: "m-stash",
	}}
}

func TestAuthenticateBuilderAdmitsBuilderToken(t *testing.T) {
	app := testBuilderApp()
	uid := bson.NewObjectID().Hex()
	token, err := app.generateJWT(uid, "builder@example.com", builderRole, bson.NilObjectID.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	called := false
	handler := app.authenticateBuilder(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		called = true
		if claims.UID != uid {
			t.Fatalf("claims.UID = %s, want %s", claims.UID, uid)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/builder/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if !called {
		t.Fatalf("builder handler was not called; status %d", rec.Code)
	}
}

func TestAuthenticateBuilderRejectsAppUserToken(t *testing.T) {
	app := testBuilderApp()
	token, err := app.generateJWT(bson.NewObjectID().Hex(), "user@example.com", "user", bson.NewObjectID().Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	handler := app.authenticateBuilder(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		t.Fatal("an app-user token must never reach a builder handler")
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/builder/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAuthenticateBuilderRejectsBuilderTokenScopedToApp(t *testing.T) {
	app := testBuilderApp()
	token, err := app.generateJWT(bson.NewObjectID().Hex(), "builder@example.com", builderRole, bson.NewObjectID().Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	handler := app.authenticateBuilder(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		t.Fatal("a builder token carrying an app id must be rejected")
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/builder/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestIsControlPlaneRole(t *testing.T) {
	for _, role := range []string{builderRole, operatorRole} {
		if !isControlPlaneRole(role) {
			t.Fatalf("%q should be a control-plane role", role)
		}
	}
	for _, role := range []string{"user", "admin", "service", ""} {
		if isControlPlaneRole(role) {
			t.Fatalf("%q must not be a control-plane role", role)
		}
	}
}

func TestAuthenticateBuilderAdmitsOperatorToken(t *testing.T) {
	app := testBuilderApp()
	token, err := app.generateJWT(bson.NewObjectID().Hex(), "operator@example.com", operatorRole, bson.NilObjectID.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	called := false
	handler := app.authenticateBuilder(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		called = true
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/builder/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("an operator token must be admitted by the builder guard")
	}
}

func TestAuthenticateOperatorAdmitsOperatorToken(t *testing.T) {
	app := testBuilderApp()
	uid := bson.NewObjectID().Hex()
	token, err := app.generateJWT(uid, "operator@example.com", operatorRole, bson.NilObjectID.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	called := false
	handler := app.authenticateOperator(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		called = true
		if claims.UID != uid {
			t.Fatalf("claims.UID = %s, want %s", claims.UID, uid)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if !called {
		t.Fatalf("operator handler was not called; status %d", rec.Code)
	}
}

func TestAuthenticateOperatorRejectsBuilderToken(t *testing.T) {
	app := testBuilderApp()
	token, err := app.generateJWT(bson.NewObjectID().Hex(), "builder@example.com", builderRole, bson.NilObjectID.Hex())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	handler := app.authenticateOperator(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		t.Fatal("a plain builder token must never reach an operator handler")
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestProvisionAppInputCarriesOwner(t *testing.T) {
	owner := bson.NewObjectID()
	input := provisionAppInput{Slug: "Arcade", DisplayName: "Arcade", OwnerID: owner}
	if err := input.normalizeAndValidate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if input.OwnerID != owner {
		t.Fatalf("owner id was not preserved through validation")
	}
	if input.Slug != "arcade" {
		t.Fatalf("slug = %s, want arcade", input.Slug)
	}
}

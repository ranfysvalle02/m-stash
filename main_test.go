package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseResourcePageCursorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	id := bson.NewObjectID()
	cursor := encodeResourceCursor(createdAt, id)
	req := httptest.NewRequest("GET", "/v1/public/ada/project?limit=7&cursor="+cursor, nil)

	limit, parsedCursor, err := parseResourcePage(req)
	if err != nil {
		t.Fatalf("parseResourcePage() error = %v", err)
	}
	if limit != 7 {
		t.Fatalf("limit = %d, want 7", limit)
	}
	if parsedCursor == nil || !parsedCursor.CreatedAt.Equal(createdAt) || parsedCursor.ID != id.Hex() {
		t.Fatalf("parsed cursor = %#v, want createdAt %s and id %s", parsedCursor, createdAt, id.Hex())
	}
}

func TestParseResourcePageRejectsInvalidLimit(t *testing.T) {
	for _, rawLimit := range []string{"0", "101", "invalid"} {
		req := httptest.NewRequest("GET", "/v1/public/ada/project?limit="+rawLimit, nil)
		if _, _, err := parseResourcePage(req); err == nil {
			t.Errorf("limit %q was accepted", rawLimit)
		}
	}
}

func TestWriteResourcePageEncodesEmptyDataAsArray(t *testing.T) {
	response := httptest.NewRecorder()
	writeResourcePage(response, make([]resourcePreview, 0), defaultResourcePageSize)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(payload.Data) != "[]" {
		t.Fatalf("empty data = %s, want []", payload.Data)
	}
}

func TestResourceCursorFilterUsesObjectID(t *testing.T) {
	createdAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	id := bson.NewObjectID()
	filter, err := resourceCursorFilter(&resourcePageCursor{CreatedAt: createdAt, ID: id.Hex()})
	if err != nil {
		t.Fatalf("resourceCursorFilter() error = %v", err)
	}

	conditions := filter["$or"].(bson.A)
	tieBreak := conditions[1].(bson.M)
	lessThan := tieBreak["_id"].(bson.M)["$lt"]
	if got, ok := lessThan.(bson.ObjectID); !ok || got != id {
		t.Fatalf("cursor _id type/value = %#v, want bson.ObjectID %s", lessThan, id.Hex())
	}
}

func TestNewOutboxEvent(t *testing.T) {
	occurredAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	eventID := bson.NewObjectID()
	event := newOutboxEvent(
		eventID,
		occurredAt,
		"request-123",
		&Claims{UID: "507f1f77bcf86cd799439011", Role: "user"},
		resourceCollectionName,
		&mutationEvent{
			Type:        "m-stash.resources.updateOne.v1",
			ResourceID:  "507f1f77bcf86cd799439012",
			NamespaceID: "507f1f77bcf86cd799439013",
			Data:        bson.M{"changedFields": []string{"title"}},
		},
	)

	if event.ID != eventID || event.RequestID != "request-123" || !event.OccurredAt.Equal(occurredAt) {
		t.Fatalf("event identity = %#v", event)
	}
	if event.SchemaVersion != outboxSchemaVersion || event.Delivery.State != "pending" || event.Delivery.Attempts != 0 {
		t.Fatalf("event delivery contract = %#v", event)
	}
	if event.Actor.ID != "507f1f77bcf86cd799439011" || event.Resource.Collection != resourceCollectionName || event.Resource.ID == "" || event.NamespaceID != "507f1f77bcf86cd799439013" {
		t.Fatalf("event actor/resource = %#v", event)
	}
}

func TestRateLimitIdentifierIsOpaqueAndWindowScoped(t *testing.T) {
	windowStart := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	first := rateLimitIdentifier("login", "203.0.113.5", windowStart)
	second := rateLimitIdentifier("login", "203.0.113.5", windowStart)
	if first != second || len(first) != 64 || strings.Contains(first, "203.0.113.5") {
		t.Fatalf("rate limit identifier = %q", first)
	}
	if first == rateLimitIdentifier("signup", "203.0.113.5", windowStart) {
		t.Fatal("rate limit identifier does not separate scopes")
	}
}

func TestAuthenticateRequiresConfiguredIssuerAndAudience(t *testing.T) {
	app := newApplication(Config{JWTSecret: "test-secret", JWTIssuer: "test-issuer", JWTAudience: "test-audience"}, nil, nil)
	token, err := app.generateJWT("507f1f77bcf86cd799439011", "ada@example.com", "user")
	if err != nil {
		t.Fatalf("generateJWT() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		if claims.Subject != claims.UID {
			t.Errorf("subject %q does not match uid %q", claims.Subject, claims.UID)
		}
		w.WriteHeader(http.StatusNoContent)
	})(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid token status = %d, want %d", response.Code, http.StatusNoContent)
	}

	invalidClaims := Claims{
		UID:   "507f1f77bcf86cd799439011",
		Email: "ada@example.com",
		Role:  "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   "507f1f77bcf86cd799439011",
			Audience:  jwt.ClaimStrings{"wrong-audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	invalidToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, invalidClaims).SignedString([]byte(app.config.JWTSecret))
	if err != nil {
		t.Fatalf("sign invalid token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+invalidToken)
	response = httptest.NewRecorder()
	app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		w.WriteHeader(http.StatusNoContent)
	})(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("wrong-audience token status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestServiceCredentialIsLimitedToSharedResourceMutations(t *testing.T) {
	app := newApplication(Config{ServiceToken: "test-service-token-with-at-least-32-characters"}, nil, nil)
	handled := false
	handler := app.authenticateSharedResource(func(w http.ResponseWriter, _ *http.Request, claims *Claims) {
		handled = true
		if claims.Role != sharedServiceRole || claims.UID != bson.NilObjectID.Hex() {
			t.Fatalf("service claims = %#v", claims)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mutation := httptest.NewRequest(http.MethodPost, "/v1/shared/resources/score", nil)
	mutation.Header.Set("Authorization", "Bearer "+app.config.ServiceToken)
	mutationResponse := httptest.NewRecorder()
	handler(mutationResponse, mutation)
	if !handled || mutationResponse.Code != http.StatusNoContent {
		t.Fatalf("service mutation handled=%t status=%d", handled, mutationResponse.Code)
	}

	handled = false
	read := httptest.NewRequest(http.MethodGet, "/v1/shared/resources/score", nil)
	read.Header.Set("Authorization", "Bearer "+app.config.ServiceToken)
	readResponse := httptest.NewRecorder()
	handler(readResponse, read)
	if handled || readResponse.Code != http.StatusForbidden {
		t.Fatalf("service read handled=%t status=%d", handled, readResponse.Code)
	}
}

func TestServiceManifest(t *testing.T) {
	app := newApplication(Config{JWTIssuer: "test-issuer", JWTAudience: "test-audience"}, nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/.well-known/m-stash.json", nil)
	response := httptest.NewRecorder()
	app.handleServiceManifest(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want %d", response.Code, http.StatusOK)
	}
	var manifest map[string]any
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest["apiVersion"] != "v1" {
		t.Fatalf("apiVersion = %v, want v1", manifest["apiVersion"])
	}
	capabilities := manifest["capabilities"].(map[string]any)
	namespace := capabilities["namespace"].(map[string]any)
	if namespace["resources"] != "/v1/me/resources/{type}/{slug}" || namespace["public"] != "/v1/public/{username}/{type}/{slug}" {
		t.Fatalf("namespace capability = %#v", namespace)
	}
	sharedNamespace := capabilities["sharedNamespace"].(map[string]any)
	if sharedNamespace["publicResources"] != "/v1/public/shared/resources/{type}/{slug}" {
		t.Fatalf("shared namespace capability = %#v", sharedNamespace)
	}
	outbox := capabilities["eventOutbox"].(map[string]any)
	if outbox["delivery"] != "transactional-outbox" || outbox["schemaVersion"] != outboxSchemaVersion {
		t.Fatalf("outbox capability = %#v", outbox)
	}
}

func TestApplicationHandlerHealth(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
	var health map[string]string
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("health status payload = %q, want ok", health["status"])
	}
}

func TestMetricsEndpointRequiresToken(t *testing.T) {
	app := newApplication(Config{MetricsToken: "test-metrics-token-with-at-least-32-characters"}, nil, nil)
	app.metrics.recordOutboxEvent("m-stash.resources.insertOne.v1")
	app.metrics.recordRateLimitRejection()

	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	unauthorizedResponse := httptest.NewRecorder()
	app.handleMetrics(unauthorizedResponse, unauthorizedRequest)
	if unauthorizedResponse.Code != http.StatusNotFound {
		t.Fatalf("unauthorized metrics status = %d, want %d", unauthorizedResponse.Code, http.StatusNotFound)
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+app.config.MetricsToken)
	response := httptest.NewRecorder()
	app.handleMetrics(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("metrics content type = %q", contentType)
	}
	if body := response.Body.String(); !strings.Contains(body, "m_stash_outbox_events_total{type=\"m-stash.resources.insertOne.v1\"} 1") {
		t.Fatalf("outbox metric missing from response: %s", body)
	} else if !strings.Contains(body, "m_stash_auth_rate_limit_rejections_total 1") {
		t.Fatalf("rate-limit metric missing from response: %s", body)
	}
}

func TestApplicationHandlerDoesNotExposeRetiredGatewayRoute(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/db/resources/find", nil)
	response := httptest.NewRecorder()
	app.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired gateway status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

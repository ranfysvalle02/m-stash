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

func TestParsePublicStashPageCursorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	id := bson.NewObjectID()
	cursor := encodePublicStashCursor(createdAt, id)
	req := httptest.NewRequest("GET", "/v1/public/profiles/ada/stashes?limit=7&cursor="+cursor, nil)

	limit, parsedCursor, err := parsePublicStashPage(req)
	if err != nil {
		t.Fatalf("parsePublicStashPage() error = %v", err)
	}
	if limit != 7 {
		t.Fatalf("limit = %d, want 7", limit)
	}
	if parsedCursor == nil || !parsedCursor.CreatedAt.Equal(createdAt) || parsedCursor.ID != id.Hex() {
		t.Fatalf("parsed cursor = %#v, want createdAt %s and id %s", parsedCursor, createdAt, id.Hex())
	}
}

func TestParsePublicStashPageRejectsInvalidLimit(t *testing.T) {
	for _, rawLimit := range []string{"0", "101", "invalid"} {
		req := httptest.NewRequest("GET", "/v1/public/profiles/ada/stashes?limit="+rawLimit, nil)
		if _, _, err := parsePublicStashPage(req); err == nil {
			t.Errorf("limit %q was accepted", rawLimit)
		}
	}
}

func TestPublicStashCursorFilterUsesObjectID(t *testing.T) {
	createdAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	id := bson.NewObjectID()
	filter, err := publicStashCursorFilter(&publicStashCursor{CreatedAt: createdAt, ID: id.Hex()})
	if err != nil {
		t.Fatalf("publicStashCursorFilter() error = %v", err)
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
		"stashes",
		&mutationEvent{
			Type:       "m-stash.stashes.updateOne.v1",
			ResourceID: "507f1f77bcf86cd799439012",
			Data:       bson.M{"changedFields": []string{"title"}},
		},
	)

	if event.ID != eventID || event.RequestID != "request-123" || !event.OccurredAt.Equal(occurredAt) {
		t.Fatalf("event identity = %#v", event)
	}
	if event.SchemaVersion != outboxSchemaVersion || event.Delivery.State != "pending" || event.Delivery.Attempts != 0 {
		t.Fatalf("event delivery contract = %#v", event)
	}
	if event.Actor.ID != "507f1f77bcf86cd799439011" || event.Resource.Collection != "stashes" || event.Resource.ID == "" {
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

func TestValidateClientQuery(t *testing.T) {
	if err := validateClientQuery(map[string]any{"tags": map[string]any{"$in": []any{"go", "mongo"}}}, 0); err != nil {
		t.Fatalf("allowed query rejected: %v", err)
	}
	for _, query := range []map[string]any{
		{"$where": "sleep(1000)"},
		{"$expr": map[string]any{"$eq": []any{1, 1}}},
	} {
		if err := validateClientQuery(query, 0); err == nil {
			t.Errorf("unsafe query %v was accepted", query)
		}
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

func TestValidateQueryLimit(t *testing.T) {
	if limit, err := validateQueryLimit(0); err != nil || limit != defaultQueryPageSize {
		t.Fatalf("default query limit = %d, %v", limit, err)
	}
	if limit, err := validateQueryLimit(maxQueryPageSize); err != nil || limit != maxQueryPageSize {
		t.Fatalf("maximum query limit = %d, %v", limit, err)
	}
	if _, err := validateQueryLimit(maxQueryPageSize + 1); err == nil {
		t.Fatal("oversized query limit was accepted")
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
	app.metrics.recordOutboxEvent("m-stash.stashes.insertOne.v1")
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
	if body := response.Body.String(); !strings.Contains(body, "m_stash_outbox_events_total{type=\"m-stash.stashes.insertOne.v1\"} 1") {
		t.Fatalf("outbox metric missing from response: %s", body)
	} else if !strings.Contains(body, "m_stash_auth_rate_limit_rejections_total 1") {
		t.Fatalf("rate-limit metric missing from response: %s", body)
	}
}

func TestDatabaseProxyBlocksInternalCollections(t *testing.T) {
	app := newApplication(Config{}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/db/_m_stash_outbox/find", nil)
	response := httptest.NewRecorder()
	app.handleDatabaseProxy(response, request, &Claims{})
	if response.Code != http.StatusForbidden {
		t.Fatalf("internal collection status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

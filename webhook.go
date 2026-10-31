package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const webhookCollectionName = "_m_stash_webhooks"

// webhookEndpoint is a per-app registration that receives signed outbox event deliveries.
type webhookEndpoint struct {
	ID         bson.ObjectID `bson:"_id" json:"id"`
	AppID      bson.ObjectID `bson:"appId" json:"-"`
	URL        string        `bson:"url" json:"url"`
	EventTypes []string      `bson:"eventTypes" json:"eventTypes"`
	Secret     string        `bson:"secret" json:"-"`
	Active     bool          `bson:"active" json:"active"`
	CreatedAt  time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type webhookInput struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
}

func (input *webhookInput) normalizeAndValidate() error {
	input.URL = strings.TrimSpace(input.URL)
	if !isAbsoluteHTTPURL(input.URL) {
		return errors.New("url must be an absolute http(s) URL")
	}
	if len(input.EventTypes) == 0 || len(input.EventTypes) > 20 {
		return errors.New("eventTypes must contain 1-20 entries")
	}
	for index, pattern := range input.EventTypes {
		pattern = strings.TrimSpace(pattern)
		if !isValidEventTypePattern(pattern) {
			return errors.New("eventTypes entries must be event type patterns like m-stash.resources or m-stash.resources.increment.v1")
		}
		input.EventTypes[index] = pattern
	}
	return nil
}

func isValidEventTypePattern(pattern string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == "" || len(pattern) > 100 {
		return false
	}
	for _, character := range pattern {
		isLower := character >= 'a' && character <= 'z'
		isUpper := character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if !isLower && !isUpper && !isDigit && character != '.' && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

// matchesEventType reports whether any registered pattern selects eventType, supporting an exact
// match, a dotted prefix (e.g. "m-stash.resources"), or the "*" wildcard.
func matchesEventType(patterns []string, eventType string) bool {
	for _, pattern := range patterns {
		if pattern == "*" || pattern == eventType {
			return true
		}
		if strings.HasPrefix(eventType, strings.TrimSuffix(pattern, ".")+".") {
			return true
		}
	}
	return false
}

func signWebhookPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookBackoff is an exponential backoff capped at maximum, keyed on the attempt number.
func webhookBackoff(attempts int, base, maximum time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= maximum {
			return maximum
		}
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func generateWebhookSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func ensureWebhookIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(webhookCollectionName).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "appId", Value: 1}, {Key: "active", Value: 1}},
	})
	return err
}

func (app *application) handleSharedWebhooks(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !canWriteSharedNamespace(claims) {
		writeError(w, http.StatusForbidden, "Only administrators can manage webhooks")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/shared/webhooks"), "/")
	switch {
	case r.Method == http.MethodGet && id == "":
		app.listWebhooks(w, r)
	case r.Method == http.MethodPost && id == "":
		app.createWebhook(w, r)
	case r.Method == http.MethodDelete && id != "":
		app.deleteWebhook(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	if err := input.normalizeAndValidate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not generate webhook secret")
		return
	}
	now := time.Now().UTC()
	appID, _ := appIDFromContext(r.Context())
	endpoint := webhookEndpoint{
		ID:         bson.NewObjectID(),
		AppID:      appID,
		URL:        input.URL,
		EventTypes: input.EventTypes,
		Secret:     secret,
		Active:     true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if _, err := app.database.Collection(webhookCollectionName).InsertOne(r.Context(), endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not register webhook")
		return
	}
	// Secret is returned only once, at creation time.
	writeJSON(w, http.StatusCreated, map[string]any{"data": endpoint, "secret": secret})
}

func (app *application) listWebhooks(w http.ResponseWriter, r *http.Request) {
	appID, _ := appIDFromContext(r.Context())
	cursor, err := app.database.Collection(webhookCollectionName).Find(
		r.Context(),
		bson.M{"appId": appID},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load webhooks")
		return
	}
	defer cursor.Close(r.Context())
	endpoints := make([]webhookEndpoint, 0)
	if err := cursor.All(r.Context(), &endpoints); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode webhooks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": endpoints})
}

func (app *application) deleteWebhook(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}
	appID, _ := appIDFromContext(r.Context())
	result, err := app.database.Collection(webhookCollectionName).DeleteOne(r.Context(), bson.M{"_id": id, "appId": appID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete webhook")
		return
	}
	if result.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

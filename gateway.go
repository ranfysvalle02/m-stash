package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"nhooyr.io/websocket"
)

const (
	defaultQueryPageSize = 50
	maxQueryPageSize     = 100
)

func (app *application) handleDatabaseProxy(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "Invalid endpoint path. Use /v1/db/{collection}/{action}")
		return
	}
	collectionName, action := parts[2], parts[3]
	if collectionName == outboxCollectionName || strings.HasPrefix(collectionName, "_m_stash_") {
		writeError(w, http.StatusForbidden, "Access to internal collections is restricted")
		return
	}

	rule, exists := app.config.Rules[collectionName]
	if !exists {
		writeError(w, http.StatusForbidden, fmt.Sprintf("Access to collection '%s' is restricted", collectionName))
		return
	}

	var body struct {
		Query   map[string]any `json:"query"`
		Payload map[string]any `json:"payload"`
		Limit   int            `json:"limit"`
	}
	if !decodeJSONBody(w, r, &body, true) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	collection := app.database.Collection(collectionName)

	switch action {
	case "find":
		limit, err := validateQueryLimit(body.Limit)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		cursor, err := collection.Find(ctx, filter, options.Find().SetLimit(int64(limit)))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var results []bson.M
		if err := cursor.All(ctx, &results); err != nil {
			writeError(w, http.StatusInternalServerError, "Error decoding cursor results: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": results,
			"page": map[string]int{"limit": limit},
		})

	case "findOne":
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		var result bson.M
		if err := collection.FindOne(ctx, filter).Decode(&result); err != nil {
			writeError(w, http.StatusNotFound, "Document not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case "insertOne":
		validatedPayload, err := authorizeAndHydrateInsert(body.Payload, rule, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if collectionName == "profiles" {
			if err := validateProfilePayload(validatedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if collectionName == "stashes" {
			if err := validateStashPayload(validatedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			validatedPayload["createdAt"] = time.Now().UTC()
			validatedPayload["updatedAt"] = time.Now().UTC()
		}

		result, err := app.executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			insertResult, err := collection.InsertOne(transactionContext, validatedPayload)
			if err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{
				Result: insertResult,
				Event: &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: objectIDString(insertResult.InsertedID),
					Data:       bson.M{"fields": fieldNames(validatedPayload)},
				},
			}, nil
		})
		if err != nil {
			app.logger.Error("database insert failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not create document")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": result})

	case "updateOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		sanitizedPayload, err := sanitizeUpdatePayload(body.Payload, rule, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if collectionName == "profiles" {
			if err := validateProfilePayload(sanitizedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if collectionName == "stashes" {
			if title, exists := sanitizedPayload["title"]; exists {
				if err := validateStashPayload(map[string]any{"title": title}); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			sanitizedPayload["updatedAt"] = time.Now().UTC()
		}

		result, err := app.executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			var target struct {
				ID bson.ObjectID `bson:"_id"`
			}
			lookupErr := collection.FindOne(transactionContext, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&target)
			if lookupErr != nil && !errors.Is(lookupErr, mongo.ErrNoDocuments) {
				return mutationOutcome{}, lookupErr
			}
			updateResult, err := collection.UpdateOne(transactionContext, filter, bson.M{"$set": sanitizedPayload})
			if err != nil {
				return mutationOutcome{}, err
			}
			outcome := mutationOutcome{Result: updateResult}
			if updateResult.MatchedCount > 0 {
				if lookupErr != nil {
					return mutationOutcome{}, errors.New("could not identify updated document for outbox event")
				}
				outcome.Event = &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: target.ID.Hex(),
					Data: bson.M{
						"changedFields": fieldNames(sanitizedPayload),
						"matchedCount":  updateResult.MatchedCount,
						"modifiedCount": updateResult.ModifiedCount,
					},
				}
			}
			return outcome, nil
		})
		if err != nil {
			app.logger.Error("database update failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not update document")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case "deleteOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		result, err := app.executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			var target struct {
				ID bson.ObjectID `bson:"_id"`
			}
			lookupErr := collection.FindOne(transactionContext, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&target)
			if lookupErr != nil && !errors.Is(lookupErr, mongo.ErrNoDocuments) {
				return mutationOutcome{}, lookupErr
			}
			deleteResult, err := collection.DeleteOne(transactionContext, filter)
			if err != nil {
				return mutationOutcome{}, err
			}
			outcome := mutationOutcome{Result: deleteResult}
			if deleteResult.DeletedCount > 0 {
				if lookupErr != nil {
					return mutationOutcome{}, errors.New("could not identify deleted document for outbox event")
				}
				outcome.Event = &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: target.ID.Hex(),
					Data:       bson.M{"deletedCount": deleteResult.DeletedCount},
				}
			}
			return outcome, nil
		})
		if err != nil {
			app.logger.Error("database delete failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not delete document")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported action '%s'", action))
	}
}

type websocketResponseWriter struct {
	header http.Header
	body   bytes.Buffer
}

func newWebsocketResponseWriter() *websocketResponseWriter {
	return &websocketResponseWriter{header: make(http.Header)}
}

func (writer *websocketResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *websocketResponseWriter) Write(body []byte) (int, error) {
	return writer.body.Write(body)
}

func (writer *websocketResponseWriter) WriteHeader(_ int) {}

func (app *application) handleDatabaseWebSocket(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !app.originAllowed(r.Header.Get("Origin")) {
		writeError(w, http.StatusForbidden, "Origin is not allowed")
		return
	}

	collectionName := strings.TrimPrefix(r.URL.Path, "/v1/ws/")
	if collectionName == "" || strings.Contains(collectionName, "/") {
		writeError(w, http.StatusBadRequest, "Use /v1/ws/{collection}")
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	for {
		messageType, message, err := connection.Read(r.Context())
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			_ = connection.Close(websocket.StatusUnsupportedData, "text JSON messages required")
			return
		}

		var request struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(message, &request); err != nil || request.Action == "" || strings.Contains(request.Action, "/") {
			if err := connection.Write(r.Context(), websocket.MessageText, []byte(`{"error":"Messages require a valid action"}`)); err != nil {
				return
			}
			continue
		}

		proxyRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/db/"+collectionName+"/"+request.Action, bytes.NewReader(message))
		if err != nil {
			return
		}
		response := newWebsocketResponseWriter()
		app.handleDatabaseProxy(response, proxyRequest, claims)
		if err := connection.Write(r.Context(), websocket.MessageText, response.body.Bytes()); err != nil {
			return
		}
	}
}

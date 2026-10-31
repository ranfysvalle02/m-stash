package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// webhookWorker drains the transactional outbox and delivers signed events to registered
// endpoints. Delivery is at-least-once: receivers dedupe on the event id. Claims are atomic,
// so multiple instances can run safely.
type webhookWorker struct {
	app         *application
	client      *http.Client
	interval    time.Duration
	claimTTL    time.Duration
	baseBackoff time.Duration
	maxBackoff  time.Duration
	maxAttempts int
	batchSize   int
	stop        chan struct{}
	done        chan struct{}
}

func newWebhookWorker(app *application) *webhookWorker {
	return &webhookWorker{
		app:         app,
		client:      &http.Client{Timeout: 10 * time.Second},
		interval:    app.config.WebhookPollInterval,
		claimTTL:    time.Minute,
		baseBackoff: 10 * time.Second,
		maxBackoff:  time.Hour,
		maxAttempts: app.config.WebhookMaxAttempts,
		batchSize:   50,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

func (worker *webhookWorker) Start() {
	go worker.run()
}

func (worker *webhookWorker) Stop(ctx context.Context) {
	close(worker.stop)
	select {
	case <-worker.done:
	case <-ctx.Done():
	}
}

func (worker *webhookWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-worker.stop:
			return
		case <-ticker.C:
			worker.drain(context.Background())
		}
	}
}

func (worker *webhookWorker) drain(ctx context.Context) {
	for processed := 0; processed < worker.batchSize; processed++ {
		select {
		case <-worker.stop:
			return
		default:
		}
		event, claimed, err := worker.claimNext(ctx)
		if err != nil {
			worker.app.logger.Error("webhook claim failed", "error", err)
			return
		}
		if !claimed {
			return
		}
		worker.deliver(ctx, event)
	}
}

func (worker *webhookWorker) claimNext(ctx context.Context) (outboxEvent, bool, error) {
	now := time.Now().UTC()
	filter := bson.M{
		"delivery.state":       bson.M{"$in": bson.A{"pending", "retry", "sending"}},
		"delivery.availableAt": bson.M{"$lte": now},
	}
	update := bson.M{"$set": bson.M{"delivery.state": "sending", "delivery.availableAt": now.Add(worker.claimTTL)}}
	findOptions := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "delivery.availableAt", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)
	var event outboxEvent
	err := worker.app.database.Collection(outboxCollectionName).FindOneAndUpdate(ctx, filter, update, findOptions).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return outboxEvent{}, false, nil
	}
	if err != nil {
		return outboxEvent{}, false, err
	}
	return event, true, nil
}

func (worker *webhookWorker) deliver(ctx context.Context, event outboxEvent) {
	endpoints, err := worker.matchingEndpoints(ctx, event.AppID, event.Type)
	if err != nil {
		worker.app.logger.Error("webhook endpoint lookup failed", "error", err, "eventId", event.ID.Hex())
		worker.reschedule(ctx, event, "endpoint lookup failed")
		return
	}
	if len(endpoints) == 0 {
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "delivered", "delivery.availableAt": time.Now().UTC()}})
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		worker.app.logger.Error("webhook payload marshal failed", "error", err, "eventId", event.ID.Hex())
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "dead"}})
		return
	}
	delivered := true
	for _, endpoint := range endpoints {
		if err := worker.post(ctx, endpoint, event, payload); err != nil {
			delivered = false
			worker.app.logger.Warn("webhook delivery failed", "error", err, "url", endpoint.URL, "eventId", event.ID.Hex())
		}
	}
	if delivered {
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "delivered", "delivery.availableAt": time.Now().UTC()}})
		return
	}
	worker.reschedule(ctx, event, "delivery failed")
}

func (worker *webhookWorker) matchingEndpoints(ctx context.Context, appIDHex, eventType string) ([]webhookEndpoint, error) {
	appID, err := bson.ObjectIDFromHex(appIDHex)
	if err != nil {
		return nil, nil
	}
	cursor, err := worker.app.database.Collection(webhookCollectionName).Find(ctx, bson.M{"appId": appID, "active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var endpoints []webhookEndpoint
	if err := cursor.All(ctx, &endpoints); err != nil {
		return nil, err
	}
	matched := make([]webhookEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if matchesEventType(endpoint.EventTypes, eventType) {
			matched = append(matched, endpoint)
		}
	}
	return matched, nil
}

func (worker *webhookWorker) post(ctx context.Context, endpoint webhookEndpoint, event outboxEvent, payload []byte) error {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-M-Stash-Event-Id", event.ID.Hex())
	request.Header.Set("X-M-Stash-Event-Type", event.Type)
	request.Header.Set("X-M-Stash-Signature", signWebhookPayload(endpoint.Secret, payload))
	response, err := worker.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func (worker *webhookWorker) reschedule(ctx context.Context, event outboxEvent, reason string) {
	attempts := event.Delivery.Attempts + 1
	if attempts >= worker.maxAttempts {
		worker.app.logger.Error("webhook event dead-lettered", "eventId", event.ID.Hex(), "attempts", attempts, "reason", reason)
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "dead", "delivery.attempts": attempts}})
		return
	}
	availableAt := time.Now().UTC().Add(webhookBackoff(attempts, worker.baseBackoff, worker.maxBackoff))
	worker.setState(ctx, event.ID, bson.M{"$set": bson.M{
		"delivery.state":       "retry",
		"delivery.attempts":    attempts,
		"delivery.availableAt": availableAt,
	}})
}

func (worker *webhookWorker) setState(ctx context.Context, id bson.ObjectID, update bson.M) {
	if _, err := worker.app.database.Collection(outboxCollectionName).UpdateByID(ctx, id, update); err != nil {
		worker.app.logger.Error("webhook state update failed", "error", err, "eventId", id.Hex())
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	outboxCollectionName = "_m_stash_outbox"
	outboxSchemaVersion  = "v1"
)

type outboxActor struct {
	ID   string `bson:"id" json:"id"`
	Role string `bson:"role" json:"role"`
}

type outboxResource struct {
	Collection string `bson:"collection" json:"collection"`
	ID         string `bson:"id,omitempty" json:"id,omitempty"`
}

type outboxDelivery struct {
	State       string    `bson:"state" json:"state"`
	Attempts    int       `bson:"attempts" json:"attempts"`
	AvailableAt time.Time `bson:"availableAt" json:"availableAt"`
}

// outboxEvent is an internal, immutable contract for independently deployed workers.
type outboxEvent struct {
	ID            bson.ObjectID  `bson:"_id" json:"id"`
	Type          string         `bson:"type" json:"type"`
	SchemaVersion string         `bson:"schemaVersion" json:"schemaVersion"`
	OccurredAt    time.Time      `bson:"occurredAt" json:"occurredAt"`
	RequestID     string         `bson:"requestId" json:"requestId"`
	NamespaceID   string         `bson:"namespaceId,omitempty" json:"namespaceId,omitempty"`
	Actor         outboxActor    `bson:"actor" json:"actor"`
	Resource      outboxResource `bson:"resource" json:"resource"`
	Data          bson.M         `bson:"data" json:"data"`
	Delivery      outboxDelivery `bson:"delivery" json:"delivery"`
}

type mutationEvent struct {
	Type        string
	ResourceID  string
	NamespaceID string
	Data        bson.M
}

type mutationOutcome struct {
	Result any
	Event  *mutationEvent
}

func verifyTransactionSupport(ctx context.Context, client *mongo.Client) error {
	var hello bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return fmt.Errorf("check MongoDB transaction support: %w", err)
	}
	setName, _ := hello["setName"].(string)
	serverMessage, _ := hello["msg"].(string)
	if setName == "" && serverMessage != "isdbgrid" {
		return errors.New("MongoDB replica set or sharded cluster is required for the transactional outbox")
	}
	return nil
}

func ensureOutboxIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(outboxCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "delivery.state", Value: 1}, {Key: "delivery.availableAt", Value: 1}, {Key: "_id", Value: 1}}},
		{Keys: bson.D{{Key: "occurredAt", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "requestId", Value: 1}}},
	})
	if err != nil {
		return fmt.Errorf("create outbox indexes: %w", err)
	}
	return nil
}

func (app *application) executeMutationWithOutbox(ctx context.Context, claims *Claims, collectionName, action string, mutate func(context.Context) (mutationOutcome, error)) (any, error) {
	session, err := app.mongoClient.StartSession()
	if err != nil {
		return nil, fmt.Errorf("start MongoDB transaction: %w", err)
	}
	defer session.EndSession(ctx)

	eventID := bson.NewObjectID()
	occurredAt := time.Now().UTC()
	emittedEventType := ""
	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		emittedEventType = ""
		outcome, err := mutate(transactionContext)
		if err != nil {
			return nil, err
		}
		if outcome.Event == nil {
			return outcome.Result, nil
		}

		event := newOutboxEvent(eventID, occurredAt, requestIDFromContext(ctx), claims, collectionName, outcome.Event)
		if _, err := app.database.Collection(outboxCollectionName).InsertOne(transactionContext, event); err != nil {
			return nil, fmt.Errorf("write outbox event: %w", err)
		}
		emittedEventType = event.Type
		return outcome.Result, nil
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, err
	}
	if emittedEventType != "" {
		app.metrics.recordOutboxEvent(emittedEventType)
	}
	return result, nil
}

func newOutboxEvent(id bson.ObjectID, occurredAt time.Time, requestID string, claims *Claims, collectionName string, event *mutationEvent) outboxEvent {
	return outboxEvent{
		ID:            id,
		Type:          event.Type,
		SchemaVersion: outboxSchemaVersion,
		OccurredAt:    occurredAt,
		RequestID:     requestID,
		NamespaceID:   event.NamespaceID,
		Actor:         outboxActor{ID: claims.UID, Role: claims.Role},
		Resource:      outboxResource{Collection: collectionName, ID: event.ResourceID},
		Data:          event.Data,
		Delivery: outboxDelivery{
			State:       "pending",
			Attempts:    0,
			AvailableAt: occurredAt,
		},
	}
}

func mutationEventType(collectionName, action string) string {
	return "m-stash." + collectionName + "." + action + ".v1"
}

func fieldNames(payload map[string]any) []string {
	fields := make([]string, 0, len(payload))
	for field := range payload {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func objectIDString(id any) string {
	if objectID, ok := id.(bson.ObjectID); ok {
		return objectID.Hex()
	}
	return fmt.Sprint(id)
}

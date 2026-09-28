package main

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"golang.org/x/crypto/bcrypt"
)

func bootstrapAdmin(ctx context.Context, app *application) error {
	if app.config.AdminEmail == "" {
		return nil
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(app.config.AdminPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	now := time.Now().UTC()
	session, err := app.mongoClient.StartSession()
	if err != nil {
		return fmt.Errorf("start bootstrap admin transaction: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		_, err := app.database.Collection("_users").UpdateOne(
			transactionContext,
			bson.M{"email": app.config.AdminEmail},
			bson.M{
				"$set": bson.M{"role": "admin"},
				"$setOnInsert": bson.M{
					"_id":       bson.NewObjectID(),
					"password":  string(passwordHash),
					"createdAt": now,
				},
			},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			return nil, err
		}
		return nil, upsertSharedNamespace(transactionContext, app)
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	return nil
}

func ensureSharedNamespace(ctx context.Context, app *application) error {
	return upsertSharedNamespace(ctx, app)
}

func upsertSharedNamespace(ctx context.Context, app *application) error {
	namespace := newSharedNamespace(time.Now().UTC())
	collection := app.database.Collection(namespaceCollectionName)
	_, err := collection.UpdateOne(
		ctx,
		bson.M{"kind": sharedNamespaceKind},
		bson.M{"$setOnInsert": namespace},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			var existing Namespace
			if findErr := collection.FindOne(ctx, bson.M{"kind": sharedNamespaceKind}).Decode(&existing); findErr == nil {
				return nil
			}
		}
		return fmt.Errorf("create shared namespace: %w", err)
	}
	return nil
}

package main

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

func bootstrapAdmin(ctx context.Context, database *mongo.Database, config Config) error {
	if config.AdminEmail == "" {
		return nil
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(config.AdminPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	_, err = database.Collection("_users").UpdateOne(
		ctx,
		bson.M{"email": config.AdminEmail},
		bson.M{
			"$set": bson.M{"role": "admin"},
			"$setOnInsert": bson.M{
				"_id":       bson.NewObjectID(),
				"password":  string(passwordHash),
				"createdAt": time.Now().UTC(),
			},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("upsert bootstrap admin: %w", err)
	}
	return nil
}

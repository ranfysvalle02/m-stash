package main

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// operatorRole marks the deployment super-operator: a control-plane principal
// (nil appId) bootstrapped from ADMIN_EMAIL/ADMIN_PASSWORD. It owns apps like a
// builder but is unbounded and unlocks the deployment control plane at /admin.
//
// Per-application administrators and the shared namespace are still provisioned
// per application in provisionApp (app_registry.go); this is a separate,
// deployment-wide identity, not a return to a single-tenant admin.
const operatorRole = "operator"

// ensureBootstrapAdmin reconciles the ADMIN_EMAIL/ADMIN_PASSWORD operator on
// every boot. It is idempotent: the password is re-hashed and re-applied so an
// operator can rotate the credential by editing .env and restarting, and the
// two principals below are upserted, never duplicated.
//
//  1. A control-plane operator (nil appId, role operator) that signs into
//     /console and /admin and manages the whole deployment.
//  2. The hosted demo app's admin (scoped to the default app) so the same
//     credentials also sign into /login and manage /app/shared.
//
// When the default app has no owner yet, the operator adopts it so it appears
// in their console immediately. Both empty credentials disable the bootstrap.
func (app *application) ensureBootstrapAdmin(ctx context.Context, defaultAppID bson.ObjectID) error {
	email := normalizeEmail(app.config.AdminEmail)
	if email == "" || app.config.AdminPassword == "" {
		return nil
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(app.config.AdminPassword), bcryptCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	// 1. Control-plane operator. The (appId,email) unique index keeps this nil-app
	// principal distinct from any app-scoped user that happens to share the email.
	operator, err := app.upsertBootstrapUser(ctx, bson.NilObjectID, email, string(passwordHash), operatorRole, now, true)
	if err != nil {
		return err
	}

	if defaultAppID.IsZero() {
		return nil
	}

	// 2. Adopt the default UI app when it is unowned, so it shows up under the
	// operator's apps. The $exists guard makes a second boot a no-op and never
	// steals an app a human builder already claimed.
	var record AppRecord
	if lookupErr := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"_id": defaultAppID}).Decode(&record); lookupErr == nil {
		if record.OwnerID.IsZero() {
			if _, err := app.database.Collection(appsCollectionName).UpdateOne(
				ctx,
				bson.M{"_id": defaultAppID, "ownerId": bson.M{"$exists": false}},
				bson.M{"$set": bson.M{"ownerId": operator.ID, "updatedAt": now}},
			); err != nil {
				return err
			}
			app.appCache.delete(record.PublishableKey)
		}
	} else if lookupErr != mongo.ErrNoDocuments {
		return lookupErr
	}

	// 3. Hosted demo app admin so /login works with the same credentials.
	if _, err := app.upsertBootstrapUser(ctx, defaultAppID, email, string(passwordHash), "admin", now, false); err != nil {
		return err
	}
	return nil
}

// upsertBootstrapUser upserts a single bootstrap principal keyed on (appId,email),
// re-applying the role and password hash on every boot. When returnDoc is set it
// decodes and returns the resulting user (needed for the operator's id).
func (app *application) upsertBootstrapUser(ctx context.Context, appID bson.ObjectID, email, passwordHash, role string, now time.Time, returnDoc bool) (User, error) {
	filter := bson.M{"appId": appID, "email": email}
	update := bson.M{
		"$set": bson.M{
			"password":  passwordHash,
			"role":      role,
			"updatedAt": now,
		},
		"$setOnInsert": bson.M{
			"_id":       bson.NewObjectID(),
			"appId":     appID,
			"email":     email,
			"createdAt": now,
		},
		// A bootstrap principal is never on a bounded plan.
		"$unset": bson.M{"plan": ""},
	}
	if !returnDoc {
		_, err := app.database.Collection("_users").UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
		return User{}, err
	}
	var user User
	err := app.database.Collection("_users").FindOneAndUpdate(
		ctx, filter, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&user)
	return user, err
}

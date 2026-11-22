package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// inspectorListLimit caps how many rows the console data inspector returns per
// request. The inspector is for spot-checking and cleaning up test data, not
// paging an entire production dataset.
const inspectorListLimit = 100

// inspectedUser is the safe projection of an app's end user for the console data
// inspector. The password hash is never included.
type inspectedUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Username  string    `json:"username,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// inspectedResource is a single record surfaced in the console data inspector,
// including its decoded data and content so the builder can see exactly what a
// row holds and which namespace it lives in.
type inspectedResource struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Slug          string    `json:"slug"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary,omitempty"`
	Visibility    string    `json:"visibility"`
	NamespaceSlug string    `json:"namespaceSlug,omitempty"`
	NamespaceKind string    `json:"namespaceKind,omitempty"`
	Data          bson.M    `json:"data,omitempty"`
	Content       string    `json:"content,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ownedApp loads an app only when it belongs to the calling builder. Every data
// inspector and lifecycle handler goes through this so a builder can never read
// or mutate another owner's tenant data.
func (app *application) ownedApp(ctx context.Context, ownerID, appID bson.ObjectID) (AppRecord, error) {
	var record AppRecord
	err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"_id": appID, "ownerId": ownerID}).Decode(&record)
	return record, err
}

// deleteBuilderApp permanently removes an owned app and all of its tenant data.
// It reuses the same cascade as the sandbox sweeper so a deleted app leaves no
// orphaned users, records, sessions, or outbox entries behind, and frees a slot
// against the free-tier app cap.
func (app *application) deleteBuilderApp(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	record, err := app.ownedApp(ctx, ownerID, appID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}

	// Deactivate and evict the cache so nothing keeps serving the app mid-delete.
	if _, err := app.database.Collection(appsCollectionName).
		UpdateOne(ctx, bson.M{"_id": appID, "ownerId": ownerID}, bson.M{"$set": bson.M{"active": false}}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}
	app.appCache.delete(record.PublishableKey)

	if err := app.purgeAppTenantData(ctx, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application data")
		return
	}
	if _, err := app.database.Collection(appsCollectionName).
		DeleteOne(ctx, bson.M{"_id": appID, "ownerId": ownerID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}
	app.logger.Info("builder app deleted", "app", appID.Hex(), "owner", ownerID.Hex())
	w.WriteHeader(http.StatusNoContent)
}

// listBuilderAppUsers returns an owned app's end users (not builder accounts,
// which carry a nil appId). Each user's handle is joined from their personal
// namespace so the console can show "@username" alongside the email.
func (app *application) listBuilderAppUsers(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.ownedApp(ctx, ownerID, appID); err != nil {
		app.writeOwnedAppLookupError(w, err, "Could not read users")
		return
	}

	cursor, err := app.database.Collection("_users").Find(
		ctx,
		bson.M{"appId": appID},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(inspectorListLimit),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read users")
		return
	}
	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read users")
		return
	}

	usernameByOwner := app.personalNamespaceSlugs(ctx, appID)
	out := make([]inspectedUser, 0, len(users))
	for _, user := range users {
		out = append(out, inspectedUser{
			ID:        user.ID.Hex(),
			Email:     user.Email,
			Role:      user.Role,
			Username:  usernameByOwner[user.ID.Hex()],
			CreatedAt: user.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// deleteBuilderAppUser removes a single end user from an owned app along with
// everything scoped to them: their sessions, personal namespace, that
// namespace's records, and their namespace memberships.
func (app *application) deleteBuilderAppUser(w http.ResponseWriter, r *http.Request, ownerID, appID, userID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.ownedApp(ctx, ownerID, appID); err != nil {
		app.writeOwnedAppLookupError(w, err, "Could not delete user")
		return
	}

	// The user must belong to this app before anything is deleted.
	var target User
	if err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"_id": userID, "appId": appID}).Decode(&target); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "User not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}

	// Remove the user's personal namespace and the records it holds.
	var personal Namespace
	if err := app.database.Collection(namespaceCollectionName).
		FindOne(ctx, bson.M{"appId": appID, "kind": personalNamespaceKind, "ownerId": userID}).Decode(&personal); err == nil {
		if _, err := app.database.Collection(resourceCollectionName).
			DeleteMany(ctx, bson.M{"appId": appID, "namespaceId": personal.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not delete user")
			return
		}
		if _, err := app.database.Collection(namespaceCollectionName).
			DeleteOne(ctx, bson.M{"_id": personal.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not delete user")
			return
		}
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}

	if _, err := app.database.Collection(namespaceMembershipCollection).
		DeleteMany(ctx, bson.M{"appId": appID, "userId": userID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}
	if _, err := app.database.Collection(sessionCollectionName).
		DeleteMany(ctx, bson.M{"userId": userID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}
	if _, err := app.database.Collection("_users").
		DeleteOne(ctx, bson.M{"_id": userID, "appId": appID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listBuilderAppResources returns the records in an owned app, optionally
// filtered by type and visibility, with their namespace joined in so the
// console can show where each record lives and link public ones.
func (app *application) listBuilderAppResources(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.ownedApp(ctx, ownerID, appID); err != nil {
		app.writeOwnedAppLookupError(w, err, "Could not read records")
		return
	}

	filter := bson.M{"appId": appID}
	if resourceType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))); resourceType != "" {
		filter["type"] = resourceType
	}
	if visibility := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("visibility"))); visibility != "" {
		filter["visibility"] = visibility
	}

	cursor, err := app.database.Collection(resourceCollectionName).Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(inspectorListLimit),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read records")
		return
	}
	var records []Resource
	if err := cursor.All(ctx, &records); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read records")
		return
	}

	namespaces := app.namespacesByID(ctx, appID)
	out := make([]inspectedResource, 0, len(records))
	for _, record := range records {
		namespace := namespaces[record.NamespaceID.Hex()]
		out = append(out, inspectedResource{
			ID:            record.ID.Hex(),
			Type:          record.Type,
			Slug:          record.Slug,
			Title:         record.Title,
			Summary:       record.Summary,
			Visibility:    record.Visibility,
			NamespaceSlug: namespace.Slug,
			NamespaceKind: namespace.Kind,
			Data:          record.Data,
			Content:       record.Content,
			CreatedAt:     record.CreatedAt,
			UpdatedAt:     record.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// deleteBuilderAppResource removes a single record from an owned app.
func (app *application) deleteBuilderAppResource(w http.ResponseWriter, r *http.Request, ownerID, appID, resourceID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.ownedApp(ctx, ownerID, appID); err != nil {
		app.writeOwnedAppLookupError(w, err, "Could not delete record")
		return
	}

	result, err := app.database.Collection(resourceCollectionName).
		DeleteOne(ctx, bson.M{"_id": resourceID, "appId": appID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete record")
		return
	}
	if result.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Record not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// personalNamespaceSlugs maps a user id (hex) to that user's handle for an app,
// so user listings can show "@username" without an extra round trip per user.
func (app *application) personalNamespaceSlugs(ctx context.Context, appID bson.ObjectID) map[string]string {
	slugs := map[string]string{}
	cursor, err := app.database.Collection(namespaceCollectionName).
		Find(ctx, bson.M{"appId": appID, "kind": personalNamespaceKind})
	if err != nil {
		return slugs
	}
	var namespaces []Namespace
	if err := cursor.All(ctx, &namespaces); err != nil {
		return slugs
	}
	for _, namespace := range namespaces {
		slugs[namespace.OwnerID.Hex()] = namespace.Slug
	}
	return slugs
}

// namespacesByID maps a namespace id (hex) to the namespace for an app, used to
// join record listings to their owning namespace.
func (app *application) namespacesByID(ctx context.Context, appID bson.ObjectID) map[string]Namespace {
	byID := map[string]Namespace{}
	cursor, err := app.database.Collection(namespaceCollectionName).
		Find(ctx, bson.M{"appId": appID})
	if err != nil {
		return byID
	}
	var namespaces []Namespace
	if err := cursor.All(ctx, &namespaces); err != nil {
		return byID
	}
	for _, namespace := range namespaces {
		byID[namespace.ID.Hex()] = namespace
	}
	return byID
}

// writeOwnedAppLookupError maps an ownedApp failure to 404 (not owned / missing)
// or 500, keeping the not-found message identical whether the app is missing or
// owned by someone else so ownership is never leaked.
func (app *application) writeOwnedAppLookupError(w http.ResponseWriter, err error, serverMessage string) {
	if errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	writeError(w, http.StatusInternalServerError, serverMessage)
}

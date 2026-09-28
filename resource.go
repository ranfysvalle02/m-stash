package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultResourcePageSize = 20
	maxResourcePageSize     = 100
)

type resourcePageCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

type resourceAccess struct {
	CanWrite         bool
	VisibilityFilter bson.M
}

type resourceInput struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Content    string `json:"content"`
	Data       bson.M `json:"data"`
	Visibility string `json:"visibility"`
}

type namespaceIdentityInput struct {
	DisplayName string `json:"displayName"`
	Bio         string `json:"bio"`
	AvatarURL   string `json:"avatarURL"`
	IsPublic    bool   `json:"isPublic"`
}

type resourcePreview struct {
	ID         bson.ObjectID `bson:"_id" json:"id"`
	Type       string        `bson:"type" json:"type"`
	Slug       string        `bson:"slug" json:"slug"`
	Title      string        `bson:"title" json:"title"`
	Summary    string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Visibility string        `bson:"visibility" json:"visibility"`
	CreatedAt  time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type publicNamespace struct {
	Slug        string `bson:"slug" json:"slug"`
	DisplayName string `bson:"displayName" json:"displayName"`
	Bio         string `bson:"bio,omitempty" json:"bio,omitempty"`
	AvatarURL   string `bson:"avatarURL,omitempty" json:"avatarURL,omitempty"`
}

type publicResourcePreview struct {
	ID        bson.ObjectID `bson:"_id" json:"-"`
	Type      string        `bson:"type" json:"type"`
	Slug      string        `bson:"slug" json:"slug"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type publicResource struct {
	Type      string    `bson:"type" json:"type"`
	Slug      string    `bson:"slug" json:"slug"`
	Title     string    `bson:"title" json:"title"`
	Summary   string    `bson:"summary,omitempty" json:"summary,omitempty"`
	Content   string    `bson:"content,omitempty" json:"content,omitempty"`
	Data      bson.M    `bson:"data,omitempty" json:"data,omitempty"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

func (input *resourceInput) normalizeAndValidate(requireSlug bool) error {
	input.Slug = normalizeResourcePathSegment(input.Slug)
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Content = strings.TrimSpace(input.Content)
	input.Visibility = strings.ToLower(strings.TrimSpace(input.Visibility))
	if requireSlug {
		if err := validateResourceSlug(input.Slug); err != nil {
			return err
		}
	}
	if input.Title == "" || len(input.Title) > 200 {
		return errors.New("title must be a non-empty string up to 200 characters")
	}
	if len(input.Summary) > 500 {
		return errors.New("summary must be 500 characters or fewer")
	}
	if input.Visibility != privateVisibility && input.Visibility != authenticatedVisibility && input.Visibility != publicVisibility {
		return errors.New("visibility must be private, authenticated, or public")
	}
	return nil
}

func validateResourceVisibilityForNamespace(namespace Namespace, visibility string) error {
	if namespace.Kind == personalNamespaceKind && visibility == authenticatedVisibility {
		return errors.New("authenticated visibility is available only for shared resources")
	}
	return nil
}

func (input *namespaceIdentityInput) normalizeAndValidate() error {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Bio = strings.TrimSpace(input.Bio)
	input.AvatarURL = strings.TrimSpace(input.AvatarURL)
	if input.DisplayName == "" || len(input.DisplayName) > 80 {
		return errors.New("display name must be a non-empty string up to 80 characters")
	}
	if len(input.Bio) > 500 {
		return errors.New("bio must be 500 characters or fewer")
	}
	if input.AvatarURL != "" && !isAbsoluteHTTPURL(input.AvatarURL) {
		return errors.New("avatarURL must be an absolute http or https URL")
	}
	return nil
}

func isAbsoluteHTTPURL(rawURL string) bool {
	parsedURL, err := url.ParseRequestURI(rawURL)
	return err == nil && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != ""
}

func (app *application) personalNamespaceForClaims(ctx context.Context, claims *Claims) (Namespace, error) {
	userID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil {
		return Namespace{}, errors.New("invalid authenticated user")
	}
	var membership NamespaceMember
	if err := app.database.Collection(namespaceMembershipCollection).FindOne(
		ctx,
		bson.M{"userId": userID, "role": namespaceOwnerRole},
	).Decode(&membership); err != nil {
		return Namespace{}, err
	}
	var namespace Namespace
	if err := app.database.Collection(namespaceCollectionName).FindOne(
		ctx,
		bson.M{"_id": membership.NamespaceID, "ownerId": userID, "kind": personalNamespaceKind},
	).Decode(&namespace); err != nil {
		return Namespace{}, err
	}
	return namespace, nil
}

func (app *application) handleMeNamespace(w http.ResponseWriter, r *http.Request, claims *Claims) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	namespace, err := app.personalNamespaceForClaims(ctx, claims)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Personal namespace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load personal namespace")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": namespace})
	case http.MethodPut:
		var input namespaceIdentityInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload := bson.M{
			"displayName": input.DisplayName,
			"bio":         input.Bio,
			"avatarURL":   input.AvatarURL,
			"isPublic":    input.IsPublic,
			"updatedAt":   time.Now().UTC(),
		}
		result, err := app.executeMutationWithOutbox(ctx, claims, namespaceCollectionName, "updateOne", func(transactionContext context.Context) (mutationOutcome, error) {
			updateResult, err := app.database.Collection(namespaceCollectionName).UpdateOne(transactionContext, bson.M{"_id": namespace.ID, "ownerId": namespace.OwnerID}, bson.M{"$set": payload})
			if err != nil {
				return mutationOutcome{}, err
			}
			if updateResult.MatchedCount == 0 {
				return mutationOutcome{}, mongo.ErrNoDocuments
			}
			for key, value := range payload {
				namespaceValue, _ := value.(string)
				switch key {
				case "displayName":
					namespace.DisplayName = namespaceValue
				case "bio":
					namespace.Bio = namespaceValue
				case "avatarURL":
					namespace.AvatarURL = namespaceValue
				case "isPublic":
					namespace.IsPublic, _ = value.(bool)
				case "updatedAt":
					namespace.UpdatedAt, _ = value.(time.Time)
				}
			}
			return mutationOutcome{Result: namespace, Event: &mutationEvent{
				Type:        mutationEventType(namespaceCollectionName, "updateOne"),
				ResourceID:  namespace.ID.Hex(),
				NamespaceID: namespace.ID.Hex(),
				Data:        bson.M{"changedFields": fieldNames(payload)},
			}}, nil
		})
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Personal namespace not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not update personal namespace")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) handleMeResources(w http.ResponseWriter, r *http.Request, claims *Claims) {
	typeName, slug, ok := parseResourcePath(r.URL.Path, "/v1/me/resources/")
	if !ok {
		writeError(w, http.StatusNotFound, "Resource not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	namespace, err := app.personalNamespaceForClaims(ctx, claims)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Personal namespace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load personal namespace")
		return
	}

	if slug == "" {
		app.handleResourceCollection(w, r, claims, namespace, typeName, resourceAccess{CanWrite: true})
		return
	}
	app.handleResourceDocument(w, r, claims, namespace, typeName, slug, resourceAccess{CanWrite: true})
}

func (app *application) handleSharedNamespace(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	namespace, err := app.sharedNamespace(ctx)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Shared namespace is not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load shared namespace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": namespace})
}

func (app *application) handleSharedResources(w http.ResponseWriter, r *http.Request, claims *Claims) {
	typeName, slug, ok := parseResourcePath(r.URL.Path, "/v1/shared/resources/")
	if !ok {
		writeError(w, http.StatusNotFound, "Resource not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	namespace, err := app.sharedNamespace(ctx)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Shared namespace is not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load shared namespace")
		return
	}
	access := resourceAccess{CanWrite: canWriteSharedNamespace(claims), VisibilityFilter: visibleToAuthenticatedUser(claims)}
	if slug == "" {
		app.handleResourceCollection(w, r, claims, namespace, typeName, access)
		return
	}
	app.handleResourceDocument(w, r, claims, namespace, typeName, slug, access)
}

func parseResourcePath(path, prefix string) (string, string, bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		return "", "", false
	}
	typeName := normalizeResourcePathSegment(parts[0])
	if err := validateResourceType(typeName); err != nil {
		return "", "", false
	}
	if len(parts) == 1 {
		return typeName, "", true
	}
	slug := normalizeResourcePathSegment(parts[1])
	if err := validateResourceSlug(slug); err != nil {
		return "", "", false
	}
	return typeName, slug, true
}

func (app *application) handleResourceCollection(w http.ResponseWriter, r *http.Request, claims *Claims, namespace Namespace, typeName string, access resourceAccess) {
	switch r.Method {
	case http.MethodGet:
		limit, cursor, err := parseResourcePage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filter := bson.M{"namespaceId": namespace.ID, "type": typeName}
		if len(access.VisibilityFilter) > 0 {
			filter["visibility"] = access.VisibilityFilter
		}
		if cursor != nil {
			cursorFilter, err := resourceCursorFilter(cursor)
			if err != nil {
				writeError(w, http.StatusBadRequest, "cursor is invalid")
				return
			}
			filter["$or"] = cursorFilter["$or"]
		}
		mongoCursor, err := app.database.Collection(resourceCollectionName).Find(
			r.Context(),
			filter,
			options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit+1)).SetProjection(bson.M{
				"_id": 1, "type": 1, "slug": 1, "title": 1, "summary": 1, "visibility": 1, "createdAt": 1, "updatedAt": 1,
			}),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load resources")
			return
		}
		defer mongoCursor.Close(r.Context())
		resources := make([]resourcePreview, 0)
		if err := mongoCursor.All(r.Context(), &resources); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not decode resources")
			return
		}
		writeResourcePage(w, resources, limit)

	case http.MethodPost:
		if !access.CanWrite {
			writeError(w, http.StatusForbidden, "Only administrators can write shared resources")
			return
		}
		var input resourceInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(true); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateResourceVisibilityForNamespace(namespace, input.Visibility); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		creatorID, err := creatorIDForClaims(claims)
		if err != nil {
			writeError(w, http.StatusForbidden, "Invalid authenticated user")
			return
		}
		now := time.Now().UTC()
		resource := Resource{
			ID:          bson.NewObjectID(),
			NamespaceID: namespace.ID,
			CreatorID:   creatorID,
			Type:        typeName,
			Slug:        input.Slug,
			Title:       input.Title,
			Summary:     input.Summary,
			Content:     input.Content,
			Data:        input.Data,
			Visibility:  input.Visibility,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		_, err = app.executeMutationWithOutbox(r.Context(), claims, resourceCollectionName, "insertOne", func(transactionContext context.Context) (mutationOutcome, error) {
			if _, err := app.database.Collection(resourceCollectionName).InsertOne(transactionContext, resource); err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{Result: resource, Event: &mutationEvent{
				Type:        mutationEventType(resourceCollectionName, "insertOne"),
				ResourceID:  resource.ID.Hex(),
				NamespaceID: namespace.ID.Hex(),
				Data:        bson.M{"fields": []string{"type", "slug", "title", "summary", "content", "data", "visibility"}},
			}}, nil
		})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				writeError(w, http.StatusConflict, "A resource with this type and slug already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not create resource")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": resource})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) handleResourceDocument(w http.ResponseWriter, r *http.Request, claims *Claims, namespace Namespace, typeName, slug string, access resourceAccess) {
	filter := bson.M{"namespaceId": namespace.ID, "type": typeName, "slug": slug}
	switch r.Method {
	case http.MethodGet:
		if len(access.VisibilityFilter) > 0 {
			filter["visibility"] = access.VisibilityFilter
		}
		var resource Resource
		if err := app.database.Collection(resourceCollectionName).FindOne(r.Context(), filter).Decode(&resource); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Resource not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not load resource")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": resource})

	case http.MethodPut:
		if !access.CanWrite {
			writeError(w, http.StatusForbidden, "Only administrators can write shared resources")
			return
		}
		var input resourceInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(false); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateResourceVisibilityForNamespace(namespace, input.Visibility); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload := bson.M{
			"title": input.Title, "summary": input.Summary, "content": input.Content,
			"data": input.Data, "visibility": input.Visibility, "updatedAt": time.Now().UTC(),
		}
		result, err := app.executeMutationWithOutbox(r.Context(), claims, resourceCollectionName, "updateOne", func(transactionContext context.Context) (mutationOutcome, error) {
			updateResult, err := app.database.Collection(resourceCollectionName).UpdateOne(transactionContext, filter, bson.M{"$set": payload})
			if err != nil {
				return mutationOutcome{}, err
			}
			if updateResult.MatchedCount == 0 {
				return mutationOutcome{}, mongo.ErrNoDocuments
			}
			var resource Resource
			if err := app.database.Collection(resourceCollectionName).FindOne(transactionContext, filter).Decode(&resource); err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{Result: resource, Event: &mutationEvent{
				Type:        mutationEventType(resourceCollectionName, "updateOne"),
				ResourceID:  resource.ID.Hex(),
				NamespaceID: namespace.ID.Hex(),
				Data:        bson.M{"changedFields": fieldNames(payload)},
			}}, nil
		})
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Resource not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not update resource")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case http.MethodDelete:
		if !access.CanWrite {
			writeError(w, http.StatusForbidden, "Only administrators can write shared resources")
			return
		}
		_, err := app.executeMutationWithOutbox(r.Context(), claims, resourceCollectionName, "deleteOne", func(transactionContext context.Context) (mutationOutcome, error) {
			result, err := app.database.Collection(resourceCollectionName).DeleteOne(transactionContext, filter)
			if err != nil {
				return mutationOutcome{}, err
			}
			if result.DeletedCount == 0 {
				return mutationOutcome{}, mongo.ErrNoDocuments
			}
			return mutationOutcome{Result: result, Event: &mutationEvent{
				Type:        mutationEventType(resourceCollectionName, "deleteOne"),
				ResourceID:  slug,
				NamespaceID: namespace.ID.Hex(),
				Data:        bson.M{"deletedCount": result.DeletedCount},
			}}, nil
		})
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Resource not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not delete resource")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func creatorIDForClaims(claims *Claims) (bson.ObjectID, error) {
	if claims != nil && claims.Role == sharedServiceRole {
		return bson.NilObjectID, nil
	}
	if claims == nil {
		return bson.NilObjectID, errors.New("missing authenticated user")
	}
	return bson.ObjectIDFromHex(claims.UID)
}

func writeResourcePage(w http.ResponseWriter, resources []resourcePreview, limit int) {
	var nextCursor any
	if len(resources) > limit {
		last := resources[limit-1]
		nextCursor = encodeResourceCursor(last.CreatedAt, last.ID)
		resources = resources[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": resources,
		"page": map[string]any{"limit": limit, "nextCursor": nextCursor},
	})
}

func (app *application) handlePublicNamespace(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/public/"), "/"), "/")
	if len(parts) < 1 || len(parts) > 3 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	username := normalizeNamespaceSlug(parts[0])
	if err := validateNamespaceSlug(username); err != nil {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	var namespace Namespace
	if err := app.database.Collection(namespaceCollectionName).FindOne(
		r.Context(),
		bson.M{"slug": username, "isPublic": true},
	).Decode(&namespace); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public namespace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public namespace")
		return
	}
	if len(parts) == 1 {
		writeJSON(w, http.StatusOK, map[string]any{"data": publicNamespace{
			Slug: namespace.Slug, DisplayName: namespace.DisplayName, Bio: namespace.Bio, AvatarURL: namespace.AvatarURL,
		}})
		return
	}
	typeName := normalizeResourcePathSegment(parts[1])
	if err := validateResourceType(typeName); err != nil {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	if len(parts) == 2 {
		app.handlePublicResourceCollection(w, r, namespace, typeName)
		return
	}
	slug := normalizeResourcePathSegment(parts[2])
	if err := validateResourceSlug(slug); err != nil {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	app.handlePublicResourceDocument(w, r, namespace, typeName, slug)
}

func (app *application) handlePublicSharedResources(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	typeName, slug, ok := parseResourcePath(r.URL.Path, "/v1/public/shared/resources/")
	if !ok {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	namespace, err := app.sharedNamespace(r.Context())
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public resource not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public resources")
		return
	}
	if slug == "" {
		app.handlePublicResourceCollection(w, r, namespace, typeName)
		return
	}
	app.handlePublicResourceDocument(w, r, namespace, typeName, slug)
}

func (app *application) handlePublicResourceCollection(w http.ResponseWriter, r *http.Request, namespace Namespace, typeName string) {
	limit, cursor, err := parseResourcePage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := bson.M{"namespaceId": namespace.ID, "type": typeName, "visibility": publicVisibility}
	if cursor != nil {
		cursorFilter, err := resourceCursorFilter(cursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		filter["$or"] = cursorFilter["$or"]
	}
	cursorResult, err := app.database.Collection(resourceCollectionName).Find(
		r.Context(),
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit+1)).SetProjection(bson.M{
			"type": 1, "slug": 1, "title": 1, "summary": 1, "createdAt": 1, "updatedAt": 1,
		}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load public resources")
		return
	}
	defer cursorResult.Close(r.Context())
	resources := make([]publicResourcePreview, 0)
	if err := cursorResult.All(r.Context(), &resources); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode public resources")
		return
	}
	var nextCursor any
	if len(resources) > limit {
		last := resources[limit-1]
		nextCursor = encodeResourceCursor(last.CreatedAt, last.ID)
		resources = resources[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resources, "page": map[string]any{"limit": limit, "nextCursor": nextCursor}})
}

func (app *application) handlePublicResourceDocument(w http.ResponseWriter, r *http.Request, namespace Namespace, typeName, slug string) {
	var resource publicResource
	err := app.database.Collection(resourceCollectionName).FindOne(
		r.Context(),
		bson.M{"namespaceId": namespace.ID, "type": typeName, "slug": slug, "visibility": publicVisibility},
		options.FindOne().SetProjection(bson.M{
			"type": 1, "slug": 1, "title": 1, "summary": 1, "content": 1, "data": 1, "createdAt": 1, "updatedAt": 1,
		}),
	).Decode(&resource)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public resource not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public resource")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resource})
}

func parseResourcePage(r *http.Request) (int, *resourcePageCursor, error) {
	pageSize := defaultResourcePageSize
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > maxResourcePageSize {
			return 0, nil, fmt.Errorf("limit must be an integer between 1 and %d", maxResourcePageSize)
		}
		pageSize = parsedLimit
	}

	rawCursor := r.URL.Query().Get("cursor")
	if rawCursor == "" {
		return pageSize, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil {
		return 0, nil, errors.New("cursor is invalid")
	}
	var cursor resourcePageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() {
		return 0, nil, errors.New("cursor is invalid")
	}
	if _, err := bson.ObjectIDFromHex(cursor.ID); err != nil {
		return 0, nil, errors.New("cursor is invalid")
	}
	return pageSize, &cursor, nil
}

func encodeResourceCursor(createdAt time.Time, id bson.ObjectID) string {
	payload, _ := json.Marshal(resourcePageCursor{CreatedAt: createdAt, ID: id.Hex()})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func resourceCursorFilter(cursor *resourcePageCursor) (bson.M, error) {
	objectID, err := bson.ObjectIDFromHex(cursor.ID)
	if err != nil {
		return nil, err
	}
	return bson.M{"$or": bson.A{
		bson.M{"createdAt": bson.M{"$lt": cursor.CreatedAt}},
		bson.M{"createdAt": cursor.CreatedAt, "_id": bson.M{"$lt": objectID}},
	}}, nil
}

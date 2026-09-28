package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type profileLink struct {
	Label string `bson:"label" json:"label"`
	URL   string `bson:"url" json:"url"`
}

type creatorProfileInput struct {
	Handle      string        `json:"handle"`
	DisplayName string        `json:"displayName"`
	Bio         string        `json:"bio"`
	AvatarURL   string        `json:"avatarURL"`
	Links       []profileLink `json:"links"`
	IsPublic    bool          `json:"isPublic"`
}

type creatorProfile struct {
	ID          bson.ObjectID `bson:"_id" json:"id"`
	Handle      string        `bson:"handle" json:"handle"`
	DisplayName string        `bson:"displayName" json:"displayName"`
	Bio         string        `bson:"bio" json:"bio"`
	AvatarURL   string        `bson:"avatarURL" json:"avatarURL"`
	Links       []profileLink `bson:"links" json:"links"`
	IsPublic    bool          `bson:"isPublic" json:"isPublic"`
}

type creatorStashInput struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Content  string   `json:"content"`
	Tags     []string `json:"tags"`
	IsPublic bool     `json:"isPublic"`
}

type creatorStash struct {
	ID        bson.ObjectID `bson:"_id" json:"id"`
	OwnerID   bson.ObjectID `bson:"ownerId" json:"-"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary" json:"summary"`
	Content   string        `bson:"content" json:"content"`
	Tags      []string      `bson:"tags" json:"tags"`
	IsPublic  bool          `bson:"isPublic" json:"isPublic"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type creatorStashPreview struct {
	ID        bson.ObjectID `bson:"_id" json:"id"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Tags      []string      `bson:"tags,omitempty" json:"tags,omitempty"`
	IsPublic  bool          `bson:"isPublic" json:"isPublic"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
}

func (app *application) handleMeProfile(w http.ResponseWriter, r *http.Request, claims *Claims) {
	userID, ok := claimObjectID(w, claims)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var profile creatorProfile
		err := app.database.Collection("profiles").FindOne(ctx, bson.M{"_id": userID}).Decode(&profile)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Profile not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not load profile")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": profile})

	case http.MethodPut:
		var input creatorProfileInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		payload := bson.M{
			"handle":      input.Handle,
			"displayName": input.DisplayName,
			"bio":         input.Bio,
			"avatarURL":   input.AvatarURL,
			"links":       input.Links,
			"isPublic":    input.IsPublic,
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, err := app.executeMutationWithOutbox(ctx, claims, "profiles", "updateOne", func(transactionContext context.Context) (mutationOutcome, error) {
			result, err := app.database.Collection("profiles").UpdateOne(
				transactionContext,
				bson.M{"_id": userID},
				bson.M{"$set": payload},
				options.UpdateOne().SetUpsert(true),
			)
			if err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{
				Result: result,
				Event: &mutationEvent{
					Type:       mutationEventType("profiles", "updateOne"),
					ResourceID: userID.Hex(),
					Data:       bson.M{"changedFields": fieldNames(payload)},
				},
			}, nil
		})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				writeError(w, http.StatusConflict, "Profile handle is already taken")
				return
			}
			app.logger.Error("profile update failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not save profile")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": creatorProfileFromInput(userID, input)})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) handleMeStashes(w http.ResponseWriter, r *http.Request, claims *Claims) {
	userID, ok := claimObjectID(w, claims)
	if !ok {
		return
	}

	rawID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/me/stashes"), "/")
	if rawID == "" {
		app.handleStashCollection(w, r, claims, userID)
		return
	}
	if strings.Contains(rawID, "/") {
		writeError(w, http.StatusNotFound, "Stash not found")
		return
	}
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Stash id must be a valid ObjectID")
		return
	}
	app.handleStashDocument(w, r, claims, userID, id)
}

func (app *application) handleStashCollection(w http.ResponseWriter, r *http.Request, claims *Claims, userID bson.ObjectID) {
	switch r.Method {
	case http.MethodGet:
		pageSize, cursor, err := parsePublicStashPage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filter := bson.M{"ownerId": userID}
		status := r.URL.Query().Get("status")
		switch status {
		case "", "all":
		case "draft":
			filter["isPublic"] = false
		case "published":
			filter["isPublic"] = true
		default:
			writeError(w, http.StatusBadRequest, "status must be draft, published, or all")
			return
		}
		tag := strings.TrimSpace(r.URL.Query().Get("tag"))
		if len(tag) > 64 {
			writeError(w, http.StatusBadRequest, "tag must be 64 characters or fewer")
			return
		}
		if tag != "" {
			filter["tags"] = tag
		}
		if cursor != nil {
			cursorFilter, err := publicStashCursorFilter(cursor)
			if err != nil {
				writeError(w, http.StatusBadRequest, "cursor is invalid")
				return
			}
			filter["$or"] = cursorFilter["$or"]
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		mongoCursor, err := app.database.Collection("stashes").Find(
			ctx,
			filter,
			options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(pageSize+1)).SetProjection(bson.M{
				"_id":       1,
				"title":     1,
				"summary":   1,
				"tags":      1,
				"isPublic":  1,
				"createdAt": 1,
				"updatedAt": 1,
			}),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load stashes")
			return
		}
		defer mongoCursor.Close(ctx)
		var stashes []creatorStashPreview
		if err := mongoCursor.All(ctx, &stashes); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not decode stashes")
			return
		}
		var nextCursor any
		if len(stashes) > pageSize {
			last := stashes[pageSize-1]
			nextCursor = encodePublicStashCursor(last.CreatedAt, last.ID)
			stashes = stashes[:pageSize]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": stashes,
			"page": map[string]any{"limit": pageSize, "nextCursor": nextCursor},
		})

	case http.MethodPost:
		var input creatorStashInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		now := time.Now().UTC()
		stash := creatorStash{
			ID:        bson.NewObjectID(),
			OwnerID:   userID,
			Title:     input.Title,
			Summary:   input.Summary,
			Content:   input.Content,
			Tags:      input.Tags,
			IsPublic:  input.IsPublic,
			CreatedAt: now,
			UpdatedAt: now,
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, err := app.executeMutationWithOutbox(ctx, claims, "stashes", "insertOne", func(transactionContext context.Context) (mutationOutcome, error) {
			if _, err := app.database.Collection("stashes").InsertOne(transactionContext, stash); err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{
				Result: stash,
				Event: &mutationEvent{
					Type:       mutationEventType("stashes", "insertOne"),
					ResourceID: stash.ID.Hex(),
					Data:       bson.M{"fields": []string{"title", "summary", "content", "tags", "isPublic"}},
				},
			}, nil
		})
		if err != nil {
			app.logger.Error("stash creation failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not create stash")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": stash})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) handleStashDocument(w http.ResponseWriter, r *http.Request, claims *Claims, userID, stashID bson.ObjectID) {
	filter := bson.M{"_id": stashID, "ownerId": userID}
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var stash creatorStash
		if err := app.database.Collection("stashes").FindOne(ctx, filter).Decode(&stash); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Stash not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Could not load stash")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": stash})

	case http.MethodPut:
		var input creatorStashInput
		if !decodeJSONBody(w, r, &input, false) {
			return
		}
		if err := input.normalizeAndValidate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload := bson.M{
			"title":     input.Title,
			"summary":   input.Summary,
			"content":   input.Content,
			"tags":      input.Tags,
			"isPublic":  input.IsPublic,
			"updatedAt": time.Now().UTC(),
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := app.executeMutationWithOutbox(ctx, claims, "stashes", "updateOne", func(transactionContext context.Context) (mutationOutcome, error) {
			updateResult, err := app.database.Collection("stashes").UpdateOne(transactionContext, filter, bson.M{"$set": payload})
			if err != nil {
				return mutationOutcome{}, err
			}
			if updateResult.MatchedCount == 0 {
				return mutationOutcome{}, mongo.ErrNoDocuments
			}
			var stash creatorStash
			if err := app.database.Collection("stashes").FindOne(transactionContext, filter).Decode(&stash); err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{
				Result: stash,
				Event: &mutationEvent{
					Type:       mutationEventType("stashes", "updateOne"),
					ResourceID: stashID.Hex(),
					Data:       bson.M{"changedFields": fieldNames(payload), "matchedCount": updateResult.MatchedCount, "modifiedCount": updateResult.ModifiedCount},
				},
			}, nil
		})
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Stash not found")
				return
			}
			app.logger.Error("stash update failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not update stash")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case http.MethodDelete:
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, err := app.executeMutationWithOutbox(ctx, claims, "stashes", "deleteOne", func(transactionContext context.Context) (mutationOutcome, error) {
			result, err := app.database.Collection("stashes").DeleteOne(transactionContext, filter)
			if err != nil {
				return mutationOutcome{}, err
			}
			if result.DeletedCount == 0 {
				return mutationOutcome{}, mongo.ErrNoDocuments
			}
			return mutationOutcome{
				Result: result,
				Event: &mutationEvent{
					Type:       mutationEventType("stashes", "deleteOne"),
					ResourceID: stashID.Hex(),
					Data:       bson.M{"deletedCount": result.DeletedCount},
				},
			}, nil
		})
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusNotFound, "Stash not found")
				return
			}
			app.logger.Error("stash deletion failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not delete stash")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func claimObjectID(w http.ResponseWriter, claims *Claims) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil {
		writeError(w, http.StatusForbidden, "Invalid authenticated user")
		return bson.NilObjectID, false
	}
	return id, true
}

func creatorProfileFromInput(id bson.ObjectID, input creatorProfileInput) creatorProfile {
	return creatorProfile{
		ID:          id,
		Handle:      input.Handle,
		DisplayName: input.DisplayName,
		Bio:         input.Bio,
		AvatarURL:   input.AvatarURL,
		Links:       input.Links,
		IsPublic:    input.IsPublic,
	}
}

func (input *creatorProfileInput) normalizeAndValidate() error {
	input.Handle = strings.ToLower(strings.TrimSpace(input.Handle))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Bio = strings.TrimSpace(input.Bio)
	input.AvatarURL = strings.TrimSpace(input.AvatarURL)
	if !isProfileHandle(input.Handle) {
		return errors.New("profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
	}
	if input.DisplayName == "" || len(input.DisplayName) > 80 {
		return errors.New("display name must be a non-empty string up to 80 characters")
	}
	if len(input.Bio) > 500 {
		return errors.New("bio must be 500 characters or fewer")
	}
	if input.AvatarURL != "" && !isHTTPURL(input.AvatarURL) {
		return errors.New("avatarURL must be an absolute http or https URL")
	}
	if len(input.Links) > 8 {
		return errors.New("profiles can include up to 8 links")
	}
	for index := range input.Links {
		input.Links[index].Label = strings.TrimSpace(input.Links[index].Label)
		input.Links[index].URL = strings.TrimSpace(input.Links[index].URL)
		if input.Links[index].Label == "" || len(input.Links[index].Label) > 80 || !isHTTPURL(input.Links[index].URL) {
			return errors.New("each profile link requires a label up to 80 characters and an absolute http or https URL")
		}
	}
	return nil
}

func (input *creatorStashInput) normalizeAndValidate() error {
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	if err := validateStashPayload(map[string]any{"title": input.Title}); err != nil {
		return err
	}
	if len(input.Summary) > 500 {
		return errors.New("stash summary must be 500 characters or fewer")
	}
	if len(input.Content) > 256*1024 {
		return errors.New("stash content must be 256KB or fewer")
	}
	if len(input.Tags) > 10 {
		return errors.New("stashes can include up to 10 tags")
	}
	seen := make(map[string]struct{}, len(input.Tags))
	tags := make([]string, 0, len(input.Tags))
	for _, tag := range input.Tags {
		normalizedTag := strings.ToLower(strings.TrimSpace(tag))
		if normalizedTag == "" || len(normalizedTag) > 64 {
			return errors.New("each stash tag must contain 1-64 characters")
		}
		if _, exists := seen[normalizedTag]; exists {
			continue
		}
		seen[normalizedTag] = struct{}{}
		tags = append(tags, normalizedTag)
	}
	input.Tags = tags
	return nil
}

func isHTTPURL(rawURL string) bool {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return false
	}
	return (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != ""
}

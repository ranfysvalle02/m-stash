package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultPublicPageSize = 20
	maxPublicPageSize     = 100
)

type publicStash struct {
	ID        bson.ObjectID `bson:"_id" json:"_id"`
	OwnerID   bson.ObjectID `bson:"ownerId" json:"-"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Content   string        `bson:"content,omitempty" json:"content,omitempty"`
	Tags      []string      `bson:"tags,omitempty" json:"tags,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
	Author    *publicAuthor `bson:"-" json:"author,omitempty"`
}

type publicStashPreview struct {
	ID        bson.ObjectID `bson:"_id" json:"_id"`
	OwnerID   bson.ObjectID `bson:"ownerId" json:"-"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Tags      []string      `bson:"tags,omitempty" json:"tags,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
	Author    *publicAuthor `bson:"-" json:"author,omitempty"`
}

type publicAuthor struct {
	Handle      string `bson:"handle" json:"handle"`
	DisplayName string `bson:"displayName" json:"displayName"`
	AvatarURL   string `bson:"avatarURL,omitempty" json:"avatarURL,omitempty"`
}

type publicAuthorRecord struct {
	ID          bson.ObjectID `bson:"_id"`
	Handle      string        `bson:"handle"`
	DisplayName string        `bson:"displayName"`
	AvatarURL   string        `bson:"avatarURL,omitempty"`
}

type publicStashCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func (app *application) handlePublicProfile(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	pathParts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/public/profiles/"), "/"), "/")
	if len(pathParts) == 2 && pathParts[1] == "stashes" {
		app.handlePublicStashes(w, r, pathParts[0])
		return
	}
	if len(pathParts) != 1 {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	handle := pathParts[0]
	if !isProfileHandle(handle) {
		writeError(w, http.StatusBadRequest, "Profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var profile bson.M
	err := app.database.Collection("profiles").FindOne(
		ctx,
		bson.M{"handle": handle, "isPublic": true},
		options.FindOne().SetProjection(bson.M{
			"_id":         1,
			"handle":      1,
			"displayName": 1,
			"bio":         1,
			"avatarURL":   1,
			"links":       1,
		}),
	).Decode(&profile)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public profile")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": profile})
}

func (app *application) handlePublicStashes(w http.ResponseWriter, r *http.Request, handle string) {
	if !isProfileHandle(handle) {
		writeError(w, http.StatusBadRequest, "Profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
		return
	}
	pageSize, pageCursor, err := parsePublicStashPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if len(tag) > 64 {
		writeError(w, http.StatusBadRequest, "tag must be 64 characters or fewer")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var profile bson.M
	err = app.database.Collection("profiles").FindOne(
		ctx,
		bson.M{"handle": handle, "isPublic": true},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Decode(&profile)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public profile")
		return
	}

	filter := bson.M{"ownerId": profile["_id"], "isPublic": true}
	if tag != "" {
		filter["tags"] = tag
	}
	if pageCursor != nil {
		cursorFilter, err := publicStashCursorFilter(pageCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		filter["$or"] = cursorFilter["$or"]
	}

	cursor, err := app.database.Collection("stashes").Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(pageSize+1)).SetProjection(bson.M{
			"_id":       1,
			"title":     1,
			"summary":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load public stashes")
		return
	}
	defer cursor.Close(ctx)

	var stashes []publicStashPreview
	if err := cursor.All(ctx, &stashes); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode public stashes")
		return
	}
	nextCursor := nextPublicStashCursor(stashes, pageSize)
	if nextCursor != nil {
		stashes = stashes[:pageSize]
	}
	writePublicStashPage(w, stashes, pageSize, nextCursor)
}

func (app *application) handlePublicDiscovery(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	stashID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/public/stashes"), "/")
	if stashID != "" {
		app.handlePublicStashDetail(w, r, stashID)
		return
	}
	pageSize, pageCursor, err := parsePublicStashPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if len(tag) > 64 {
		writeError(w, http.StatusBadRequest, "tag must be 64 characters or fewer")
		return
	}

	filter := bson.M{"isPublic": true}
	if tag != "" {
		filter["tags"] = tag
	}
	if pageCursor != nil {
		cursorFilter, err := publicStashCursorFilter(pageCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		filter["$or"] = cursorFilter["$or"]
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cursor, err := app.database.Collection("stashes").Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(pageSize+1)).SetProjection(bson.M{
			"_id":       1,
			"ownerId":   1,
			"title":     1,
			"summary":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load public discovery feed")
		return
	}
	defer cursor.Close(ctx)

	var stashes []publicStashPreview
	if err := cursor.All(ctx, &stashes); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode public discovery feed")
		return
	}
	nextCursor := nextPublicStashCursor(stashes, pageSize)
	if nextCursor != nil {
		stashes = stashes[:pageSize]
	}
	if err := app.addPublicPreviewAuthors(ctx, stashes); err != nil {
		app.logger.Error("could not load public stash authors", "request_id", requestIDFromContext(r.Context()), "error", err)
	}
	writePublicStashPage(w, stashes, pageSize, nextCursor)
}

func nextPublicStashCursor(stashes []publicStashPreview, pageSize int) any {
	if len(stashes) <= pageSize {
		return nil
	}
	last := stashes[pageSize-1]
	return encodePublicStashCursor(last.CreatedAt, last.ID)
}

func writePublicStashPage(w http.ResponseWriter, stashes []publicStashPreview, pageSize int, nextCursor any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data": stashes,
		"page": map[string]any{
			"limit":      pageSize,
			"nextCursor": nextCursor,
		},
	})
}

func (app *application) handlePublicStashDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	if strings.Contains(rawID, "/") {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Stash id must be a valid ObjectID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var stash publicStash
	err = app.database.Collection("stashes").FindOne(
		ctx,
		bson.M{"_id": id, "isPublic": true},
		options.FindOne().SetProjection(bson.M{
			"_id":       1,
			"ownerId":   1,
			"title":     1,
			"summary":   1,
			"content":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	).Decode(&stash)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public stash not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public stash")
		return
	}
	if err := app.addPublicStashAuthor(ctx, &stash); err != nil {
		app.logger.Error("could not load public stash author", "request_id", requestIDFromContext(r.Context()), "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": stash})
}

func (app *application) addPublicPreviewAuthors(ctx context.Context, stashes []publicStashPreview) error {
	ownerIDs := make([]bson.ObjectID, 0, len(stashes))
	for _, stash := range stashes {
		if !stash.OwnerID.IsZero() {
			ownerIDs = append(ownerIDs, stash.OwnerID)
		}
	}
	authors, err := app.loadPublicAuthors(ctx, ownerIDs)
	if err != nil {
		return err
	}
	for index := range stashes {
		if author, exists := authors[stashes[index].OwnerID]; exists {
			stashes[index].Author = &author
		}
	}
	return nil
}

func (app *application) addPublicStashAuthor(ctx context.Context, stash *publicStash) error {
	authors, err := app.loadPublicAuthors(ctx, []bson.ObjectID{stash.OwnerID})
	if err != nil {
		return err
	}
	if author, exists := authors[stash.OwnerID]; exists {
		stash.Author = &author
	}
	return nil
}

func (app *application) loadPublicAuthors(ctx context.Context, ownerIDs []bson.ObjectID) (map[bson.ObjectID]publicAuthor, error) {
	authors := make(map[bson.ObjectID]publicAuthor)
	if len(ownerIDs) == 0 {
		return authors, nil
	}
	cursor, err := app.database.Collection("profiles").Find(
		ctx,
		bson.M{"_id": bson.M{"$in": ownerIDs}, "isPublic": true},
		options.Find().SetProjection(bson.M{"_id": 1, "handle": 1, "displayName": 1, "avatarURL": 1}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var records []publicAuthorRecord
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}
	for _, record := range records {
		authors[record.ID] = publicAuthor{
			Handle:      record.Handle,
			DisplayName: record.DisplayName,
			AvatarURL:   record.AvatarURL,
		}
	}
	return authors, nil
}

func parsePublicStashPage(r *http.Request) (int, *publicStashCursor, error) {
	pageSize := defaultPublicPageSize
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > maxPublicPageSize {
			return 0, nil, fmt.Errorf("limit must be an integer between 1 and %d", maxPublicPageSize)
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
	var cursor publicStashCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() {
		return 0, nil, errors.New("cursor is invalid")
	}
	if _, err := bson.ObjectIDFromHex(cursor.ID); err != nil {
		return 0, nil, errors.New("cursor is invalid")
	}
	return pageSize, &cursor, nil
}

func encodePublicStashCursor(createdAt time.Time, id bson.ObjectID) string {
	payload, _ := json.Marshal(publicStashCursor{CreatedAt: createdAt, ID: id.Hex()})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func publicStashCursorFilter(cursor *publicStashCursor) (bson.M, error) {
	objectID, err := bson.ObjectIDFromHex(cursor.ID)
	if err != nil {
		return nil, err
	}
	return bson.M{"$or": bson.A{
		bson.M{"createdAt": bson.M{"$lt": cursor.CreatedAt}},
		bson.M{"createdAt": cursor.CreatedAt, "_id": bson.M{"$lt": objectID}},
	}}, nil
}

func isProfileHandle(handle string) bool {
	if len(handle) < 3 || len(handle) > 32 {
		return false
	}
	for _, character := range handle {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return false
		}
	}
	return true
}

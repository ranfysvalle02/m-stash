package main

import (
	"context"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// builderRole marks a control-plane principal that owns apps but belongs to no
// single app. Builder accounts live in _users with a nil appId, so the existing
// (appId,email) unique index keeps them isolated from every app's users.
const builderRole = "builder"

// authenticateBuilder admits only builder principals. Control-plane routes carry
// no app context, so an app-scoped user or admin token is rejected by the role
// check and can never reach app-management endpoints.
func (app *application) authenticateBuilder(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		if claims.Role != builderRole || (claims.AppID != "" && claims.AppID != bson.NilObjectID.Hex()) {
			writeError(w, http.StatusForbidden, "Builder credentials are required")
			return
		}
		next(w, r, claims)
	})
}

// handleBuilderAccounts bootstraps a builder account. It is platform-token gated
// so the operator controls who can provision apps; there is no open signup.
func (app *application) handleBuilderAccounts(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &request, false) {
		return
	}
	request.Email = normalizeEmail(request.Email)
	if err := validateSignUpCredentials(request.Email, request.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error hashing password")
		return
	}
	builder := User{
		ID:           bson.NewObjectID(),
		AppID:        bson.NilObjectID,
		Email:        request.Email,
		PasswordHash: string(passwordHash),
		Role:         builderRole,
		CreatedAt:    time.Now().UTC(),
	}
	if _, err := app.database.Collection("_users").InsertOne(ctx, builder); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeError(w, http.StatusConflict, "A builder with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not create builder account")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{"id": builder.ID.Hex(), "email": builder.Email},
	})
}

// handleBuilderLogin issues a builder session. It never resolves an app, so the
// resulting token's nil appId keeps it out of app-scoped data routes.
func (app *application) handleBuilderLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &request, false) {
		return
	}
	request.Email = normalizeEmail(request.Email)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var builder User
	err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"appId": bson.NilObjectID, "email": request.Email, "role": builderRole}).
		Decode(&builder)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(builder.PasswordHash), []byte(request.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	token, err := app.generateJWT(builder.ID.Hex(), builder.Email, builder.Role, bson.NilObjectID.Hex())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	if err := app.issueBrowserSession(ctx, w, builder, bson.NilObjectID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create builder session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"builder": map[string]any{"id": builder.ID.Hex(), "email": builder.Email},
	})
}

func handleBuilderMe(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"id": claims.UID, "email": claims.Email},
	})
}

func (app *application) handleBuilderApps(w http.ResponseWriter, r *http.Request, claims *Claims) {
	ownerID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil || ownerID.IsZero() {
		writeError(w, http.StatusForbidden, "Invalid builder identity")
		return
	}
	switch r.Method {
	case http.MethodGet:
		app.listBuilderApps(w, r, ownerID)
	case http.MethodPost:
		app.createBuilderApp(w, r, ownerID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) listBuilderApps(w http.ResponseWriter, r *http.Request, ownerID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cursor, err := app.database.Collection(appsCollectionName).Find(
		ctx,
		bson.M{"ownerId": ownerID},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(200),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list applications")
		return
	}
	records := []AppRecord{}
	if err := cursor.All(ctx, &records); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list applications")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": records})
}

func (app *application) createBuilderApp(w http.ResponseWriter, r *http.Request, ownerID bson.ObjectID) {
	var input provisionAppInput
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	if err := input.normalizeAndValidate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.OwnerID = ownerID

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	record, secretKey, admin, err := app.provisionApp(ctx, input)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeError(w, http.StatusConflict, "An application with this slug already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not provision application")
		return
	}
	// The secret key is returned once, at creation time, and never stored in plaintext.
	writeJSON(w, http.StatusCreated, map[string]any{
		"data":      record,
		"secretKey": secretKey,
		"admin":     admin,
	})
}

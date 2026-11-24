package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// freePlan is the self-serve tier assigned to open signups. It bounds the number
// of apps a builder owns and stamps per-app quotas at creation time.
const freePlan = "free"

// authenticateBuilder admits builder and operator principals. Control-plane
// routes carry no app context, so an app-scoped user or admin token is rejected
// by the role/appId check and can never reach app-management endpoints. The
// operator is a super-builder: it manages its own apps through the same routes.
func (app *application) authenticateBuilder(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return app.authenticate(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		if !isControlPlaneRole(claims.Role) || (claims.AppID != "" && claims.AppID != bson.NilObjectID.Hex()) {
			writeError(w, http.StatusForbidden, "Builder credentials are required")
			return
		}
		next(w, r, claims)
	})
}

// isControlPlaneRole reports whether a role belongs to a control-plane principal
// (a builder or the deployment operator) rather than an app-scoped user.
func isControlPlaneRole(role string) bool {
	return role == builderRole || role == operatorRole
}

// authenticateOperator admits only the deployment super-operator. It layers on
// authenticateBuilder's role/appId isolation and additionally re-checks the
// stored role on every request, so demoting or deleting an operator in the
// database revokes their control-plane access immediately.
func (app *application) authenticateOperator(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return app.authenticateBuilder(func(w http.ResponseWriter, r *http.Request, claims *Claims) {
		if claims.Role != operatorRole {
			writeError(w, http.StatusForbidden, "Operator credentials are required")
			return
		}
		if app.database != nil {
			ownerID, err := bson.ObjectIDFromHex(claims.UID)
			if err != nil {
				writeError(w, http.StatusForbidden, "Invalid operator identity")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			count, err := app.database.Collection("_users").CountDocuments(ctx, bson.M{"_id": ownerID, "role": operatorRole})
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Could not verify operator")
				return
			}
			if count == 0 {
				writeError(w, http.StatusForbidden, "Operator credentials are required")
				return
			}
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
		FindOne(ctx, bson.M{"appId": bson.NilObjectID, "email": request.Email, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}}).
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
		"builder": map[string]any{"id": builder.ID.Hex(), "email": builder.Email, "operator": builder.Role == operatorRole},
	})
}

// handleBuilderSignup lets a visitor create their own builder account when the
// operator has opted in via BUILDER_SIGNUP=open. New accounts land on the free
// plan, which caps how many apps they own and the quotas of each app. A per-IP
// limiter (wired at the route) plus a global daily cap keep the open endpoint
// from being used to mass-provision accounts. Emails are not verified, so the
// flag and caps are the real controls — see the plan's documented limitation.
func (app *application) handleBuilderSignup(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	settings := app.effectiveSettings(r.Context())
	if !settings.BuilderSignupOpen {
		writeError(w, http.StatusNotFound, "Self-serve builder signup is not enabled on this deployment")
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

	// A global daily budget, on top of the per-IP limit, bounds how many new
	// builders a single open deployment can mint in a day. The ceiling is read
	// live so an operator can tighten or lift it without a restart.
	if app.builderSignupDailyLimiter != nil {
		allowed, limitErr := app.builderSignupDailyLimiter.allowWithLimit(ctx, "builder-signup-global", "global", settings.BuilderSignupDailyLimit)
		if limitErr != nil {
			app.logger.Error("builder signup daily limit check failed", "request_id", requestIDFromContext(ctx), "error", limitErr)
			writeError(w, http.StatusServiceUnavailable, "Signup is temporarily unavailable")
			return
		}
		if !allowed {
			writeError(w, http.StatusTooManyRequests, "Signups are at capacity for today. Please try again tomorrow.")
			return
		}
	}

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
		Plan:         freePlan,
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

	token, err := app.generateJWT(builder.ID.Hex(), builder.Email, builder.Role, bson.NilObjectID.Hex())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	if err := app.issueBrowserSession(ctx, w, builder, bson.NilObjectID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create builder session")
		return
	}
	app.metrics.recordBuilderSignup()
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":   token,
		"builder": map[string]any{"id": builder.ID.Hex(), "email": builder.Email, "plan": builder.Plan},
	})
}

func (app *application) handleBuilderMe(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ownerID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil {
		writeError(w, http.StatusForbidden, "Invalid builder identity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	plan := ""
	isOperator := false
	var principal User
	if err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"_id": ownerID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}}).Decode(&principal); err == nil {
		plan = principal.Plan
		isOperator = principal.Role == operatorRole
	}

	data := map[string]any{"id": claims.UID, "email": claims.Email, "plan": plan, "operator": isOperator}
	if appCount, err := app.database.Collection(appsCollectionName).
		CountDocuments(ctx, bson.M{"ownerId": ownerID}); err == nil {
		data["appCount"] = appCount
	}
	// Free-plan builders see their ceilings so the console can draw usage bars.
	if plan == freePlan {
		settings := app.effectiveSettings(ctx)
		data["maxApps"] = settings.FreeTierMaxApps
		data["limits"] = freeTierLimitsFromSettings(settings)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// builderPlan returns the stored plan for a builder principal. A platform
// (operator-provisioned) builder has an empty plan and is unbounded.
func (app *application) builderPlan(ctx context.Context, ownerID bson.ObjectID) (string, error) {
	var builder User
	if err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"_id": ownerID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}}).Decode(&builder); err != nil {
		return "", err
	}
	return builder.Plan, nil
}

// enforceFreeTierAppCap returns a non-zero HTTP status + message when a free-plan
// builder is already at their app ceiling. A zero cap disables the limit.
func (app *application) enforceFreeTierAppCap(ctx context.Context, ownerID bson.ObjectID) (int, string) {
	maxApps := app.effectiveSettings(ctx).FreeTierMaxApps
	if maxApps <= 0 {
		return 0, ""
	}
	count, err := app.database.Collection(appsCollectionName).
		CountDocuments(ctx, bson.M{"ownerId": ownerID})
	if err != nil {
		return http.StatusServiceUnavailable, "Could not verify plan"
	}
	if count >= int64(maxApps) {
		return http.StatusForbidden, fmt.Sprintf("The free plan is limited to %d apps. Delete one or upgrade to add more.", maxApps)
	}
	return 0, ""
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

	// Free-plan builders are bounded: they may own only so many apps, and each
	// app it provisions carries free-tier quotas. Platform builders are unbounded.
	plan, err := app.builderPlan(ctx, ownerID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not verify plan")
		return
	}
	if plan == freePlan {
		if status, message := app.enforceFreeTierAppCap(ctx, ownerID); status != 0 {
			writeError(w, status, message)
			return
		}
		limits := freeTierLimitsFromSettings(app.effectiveSettings(ctx))
		input.Limits = &limits
	}

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

// normalizeOrigins trims, de-blanks, and validates a list of allowed CORS
// origins, enforcing a single-value-per-entry shape and a sane upper bound. It
// is shared by app updates and sandbox claims so both apply identical rules.
func normalizeOrigins(raw []string) ([]string, error) {
	origins := make([]string, 0, len(raw))
	for _, origin := range raw {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 200 || strings.ContainsAny(trimmed, " \t\r\n") {
			return nil, errors.New("Each origin must be a single value such as https://app.example.com")
		}
		origins = append(origins, trimmed)
	}
	if len(origins) > 20 {
		return nil, errors.New("An app supports at most 20 allowed origins")
	}
	return origins, nil
}

// claimBuilderApp turns a sandbox into a live app owned by the calling builder.
// The app is identified by its publishable key and authorised by the single-use
// claim token; the public share link alone can never claim. The state flip
// happens in place so the pk_ key is unchanged — downloaded zips and pasted cURL
// keep working — and all sandbox data (users, records, the sandbox admin) is kept.
func (app *application) claimBuilderApp(w http.ResponseWriter, r *http.Request, ownerID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var input struct {
		PublishableKey string    `json:"publishableKey"`
		ClaimToken     string    `json:"claimToken"`
		AllowedOrigins *[]string `json:"allowedOrigins"`
		DisplayName    *string   `json:"displayName"`
	}
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	input.PublishableKey = strings.TrimSpace(input.PublishableKey)
	input.ClaimToken = strings.TrimSpace(input.ClaimToken)
	if input.PublishableKey == "" || input.ClaimToken == "" {
		writeError(w, http.StatusBadRequest, "publishableKey and claimToken are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// 1. Read the sandbox straight from the store (not the memoized cache) and
	// verify the claim token before any mutation.
	var sandbox AppRecord
	if err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"publishableKey": input.PublishableKey}).Decode(&sandbox); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "No app with that publishable key")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not look up app")
		return
	}
	if !verifyClaimToken(input.ClaimToken, sandbox.ClaimTokenHash) {
		writeError(w, http.StatusForbidden, "Invalid claim token")
		return
	}

	// 2. Free-tier builders are capped on how many apps they can own.
	plan, err := app.builderPlan(ctx, ownerID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not verify plan")
		return
	}
	if plan == freePlan {
		if status, message := app.enforceFreeTierAppCap(ctx, ownerID); status != 0 {
			writeError(w, status, message)
			return
		}
	}

	// 3. Validate any origins with the same rules as an app update.
	var origins []string
	if input.AllowedOrigins != nil {
		origins, err = normalizeOrigins(*input.AllowedOrigins)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	displayName := ""
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
		if displayName == "" || len(displayName) > 80 {
			writeError(w, http.StatusBadRequest, "displayName must be 1 to 80 characters")
			return
		}
	}

	// A fresh secret is minted so the claimer, not the sandbox's transient owner,
	// holds the live backend credential.
	secretKey, err := generateAppKey(secretKeyPrefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not issue secret key")
		return
	}
	secretHash, err := bcrypt.GenerateFromPassword([]byte(secretKey), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not issue secret key")
		return
	}

	now := time.Now().UTC()
	set := bson.M{
		"ownerId":       ownerID,
		"sandbox":       false,
		"secretKeyHash": string(secretHash),
		"claimedAt":     now,
		"updatedAt":     now,
	}
	// The token is single-use and the app no longer expires once it is live.
	unset := bson.M{"expiresAt": "", "claimTokenHash": ""}
	if plan == freePlan {
		set["limits"] = freeTierLimitsFromSettings(app.effectiveSettings(ctx))
	} else {
		// Operator builders are unbounded, so drop the sandbox quotas entirely.
		unset["limits"] = ""
	}
	if displayName != "" {
		set["displayName"] = displayName
	}
	if origins != nil {
		set["allowedOrigins"] = origins
	}

	// 4. Flip the state in place, but only while it is still an unclaimed, live
	// sandbox. This conditional update is the race guard against a double claim or
	// a claim landing after the sweeper has purged an expired sandbox.
	var record AppRecord
	err = app.database.Collection(appsCollectionName).FindOneAndUpdate(
		ctx,
		bson.M{
			"_id":       sandbox.ID,
			"sandbox":   true,
			"ownerId":   bson.M{"$exists": false},
			"active":    true,
			"expiresAt": bson.M{"$gt": now},
		},
		bson.M{"$set": set, "$unset": unset},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&record)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			app.classifyClaimFailure(ctx, w, sandbox.ID, now)
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not claim app")
		return
	}

	// The publishable key is unchanged; only the cached record and the secret move.
	app.appCache.delete(record.PublishableKey)
	app.metrics.recordSandboxClaim()
	app.markVibeRun(ctx, bson.M{"appId": record.ID.Hex()}, vibeEventClaim)
	// The secret key is returned once, at claim time, and never stored in plaintext.
	writeJSON(w, http.StatusOK, map[string]any{"data": record, "secretKey": secretKey})
}

// classifyClaimFailure explains why a token-valid claim still could not flip the
// app's state: either someone already claimed it (409) or it has expired (410).
func (app *application) classifyClaimFailure(ctx context.Context, w http.ResponseWriter, appID bson.ObjectID, now time.Time) {
	var current AppRecord
	if err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"_id": appID}).Decode(&current); err != nil {
		writeError(w, http.StatusConflict, "This sandbox can no longer be claimed")
		return
	}
	switch {
	case !current.Sandbox || !current.OwnerID.IsZero():
		writeError(w, http.StatusConflict, "This app has already been claimed")
	case current.ExpiresAt != nil && now.After(*current.ExpiresAt):
		writeError(w, http.StatusGone, "This sandbox has expired. Start a fresh one to try again.")
	default:
		writeError(w, http.StatusConflict, "This sandbox can no longer be claimed")
	}
}

// handleBuilderApp serves owner-scoped per-app operations: update settings and
// rotate the secret key. Every write is matched on {_id, ownerId} so a builder
// can only touch apps they own.
func (app *application) handleBuilderApp(w http.ResponseWriter, r *http.Request, claims *Claims) {
	ownerID, err := bson.ObjectIDFromHex(claims.UID)
	if err != nil || ownerID.IsZero() {
		writeError(w, http.StatusForbidden, "Invalid builder identity")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/builder/apps/"), "/")
	segments := strings.Split(rest, "/")

	// The claim endpoint identifies its target app by publishable key in the body,
	// not by an id in the path, so route it before parsing an ObjectID.
	if len(segments) == 1 && segments[0] == "claim" {
		app.claimBuilderApp(w, r, ownerID)
		return
	}

	appID, err := bson.ObjectIDFromHex(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid application id")
		return
	}

	switch {
	case len(segments) == 2 && segments[1] == "rotate-secret":
		app.rotateBuilderAppSecret(w, r, ownerID, appID)
	case len(segments) == 2 && segments[1] == "usage":
		app.builderAppUsage(w, r, ownerID, appID)
	case len(segments) == 2 && (segments[1] == "skill.md" || segments[1] == "starter.zip"):
		app.builderAppVibeAsset(w, r, ownerID, appID, segments[1])
	case len(segments) == 2 && segments[1] == "owner":
		app.builderAppOwner(w, r, ownerID, appID)
	case len(segments) == 2 && segments[1] == "users":
		app.listBuilderAppUsers(w, r, ownerID, appID)
	case len(segments) == 3 && segments[1] == "users":
		userID, err := bson.ObjectIDFromHex(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid user id")
			return
		}
		app.deleteBuilderAppUser(w, r, ownerID, appID, userID)
	case len(segments) == 2 && segments[1] == "resources":
		app.listBuilderAppResources(w, r, ownerID, appID)
	case len(segments) == 3 && segments[1] == "resources":
		resourceID, err := bson.ObjectIDFromHex(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid record id")
			return
		}
		app.deleteBuilderAppResource(w, r, ownerID, appID, resourceID)
	case len(segments) == 1 && r.Method == http.MethodPatch:
		app.updateBuilderApp(w, r, ownerID, appID)
	case len(segments) == 1 && r.Method == http.MethodDelete:
		app.deleteBuilderApp(w, r, ownerID, appID)
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

// builderAppUsage reports how much an owned app is used. Users and records both
// carry appId, so the counts stay scoped to the single tenant.
func (app *application) builderAppUsage(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var record AppRecord
	if err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"_id": appID, "ownerId": ownerID}).Decode(&record); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not read usage")
		return
	}

	users, err := app.database.Collection("_users").CountDocuments(ctx, bson.M{"appId": appID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read usage")
		return
	}
	records, err := app.database.Collection(resourceCollectionName).CountDocuments(ctx, bson.M{"appId": appID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read usage")
		return
	}
	// Surface the app's effective quotas alongside the counts so the console can
	// render usage bars. Zero fields mean unlimited.
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"users": users, "records": records, "limits": effectiveLimits(&record)},
	})
}

func (app *application) rotateBuilderAppSecret(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var record AppRecord
	if err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"_id": appID, "ownerId": ownerID}).Decode(&record); err != nil {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}

	secretKey, err := generateAppKey(secretKeyPrefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not rotate secret key")
		return
	}
	secretHash, err := bcrypt.GenerateFromPassword([]byte(secretKey), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not rotate secret key")
		return
	}
	result, err := app.database.Collection(appsCollectionName).UpdateOne(
		ctx,
		bson.M{"_id": appID, "ownerId": ownerID},
		bson.M{"$set": bson.M{"secretKeyHash": string(secretHash), "updatedAt": time.Now().UTC()}},
	)
	if err != nil || result.MatchedCount != 1 {
		writeError(w, http.StatusInternalServerError, "Could not rotate secret key")
		return
	}
	// Evict the memoized record so the previous secret stops working immediately.
	app.appCache.delete(record.PublishableKey)
	writeJSON(w, http.StatusCreated, map[string]any{"data": record, "secretKey": secretKey})
}

func (app *application) updateBuilderApp(w http.ResponseWriter, r *http.Request, ownerID, appID bson.ObjectID) {
	var input struct {
		AllowedOrigins *[]string `json:"allowedOrigins"`
		DisplayName    *string   `json:"displayName"`
		Active         *bool     `json:"active"`
	}
	if !decodeJSONBody(w, r, &input, false) {
		return
	}

	set := bson.M{"updatedAt": time.Now().UTC()}
	if input.Active != nil {
		set["active"] = *input.Active
	}
	if input.AllowedOrigins != nil {
		origins, err := normalizeOrigins(*input.AllowedOrigins)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		set["allowedOrigins"] = origins
	}
	if input.DisplayName != nil {
		displayName := strings.TrimSpace(*input.DisplayName)
		if displayName == "" || len(displayName) > 80 {
			writeError(w, http.StatusBadRequest, "displayName must be 1 to 80 characters")
			return
		}
		set["displayName"] = displayName
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var record AppRecord
	err := app.database.Collection(appsCollectionName).FindOneAndUpdate(
		ctx,
		bson.M{"_id": appID, "ownerId": ownerID},
		bson.M{"$set": set},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&record)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not update application")
		return
	}
	// Keep CORS and key checks consistent with the new settings right away.
	app.appCache.delete(record.PublishableKey)
	writeJSON(w, http.StatusOK, map[string]any{"data": record})
}

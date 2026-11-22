package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// adminListLimit bounds how many rows any operator listing returns. The console
// is for oversight and triage, not paging an entire production dataset.
const adminListLimit = 200

// settingOverrideBSONKeys maps a PATCH field name to its stored override key, so
// an operator can both set and reset individual settings.
var settingOverrideBSONKeys = map[string]string{
	"trySandbox":              "trySandbox",
	"trySandboxTTL":           "trySandboxTTL",
	"builderSignupOpen":       "builderSignupOpen",
	"builderSignupDailyLimit": "builderSignupDailyLimit",
	"demoSeed":                "demoSeed",
	"webhookDelivery":         "webhookDelivery",
	"freeTierMaxApps":         "freeTierMaxApps",
	"freeTierMaxUsers":        "freeTierMaxUsers",
	"freeTierMaxRecords":      "freeTierMaxRecords",
	"freeTierMaxRecordBytes":  "freeTierMaxRecordBytes",
	"vibeMaxSandboxes":        "vibeMaxSandboxes",
	"vibeDailyLimit":          "vibeDailyLimit",
	"vibeShareTTL":            "vibeShareTTL",
}

// appExists loads any app by id, regardless of owner. Operator routes use this
// in place of the owner-scoped ownedApp.
func (app *application) appExists(ctx context.Context, appID bson.ObjectID) (AppRecord, error) {
	var record AppRecord
	err := app.database.Collection(appsCollectionName).FindOne(ctx, bson.M{"_id": appID}).Decode(&record)
	return record, err
}

// countByAppID aggregates a tenant collection into per-app counts keyed by the
// app id hex, so the fleet view needs two queries instead of two per app.
func (app *application) countByAppID(ctx context.Context, collection string) (map[string]int64, error) {
	cursor, err := app.database.Collection(collection).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$appId"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID bson.ObjectID `bson:"_id"`
		N  int64         `bson:"n"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.ID.Hex()] = row.N
	}
	return counts, nil
}

// appCountByOwner aggregates how many apps each control-plane principal owns.
func (app *application) appCountByOwner(ctx context.Context) (map[string]int64, error) {
	cursor, err := app.database.Collection(appsCollectionName).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "ownerId", Value: bson.D{{Key: "$exists", Value: true}, {Key: "$ne", Value: nil}}}}}},
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$ownerId"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID bson.ObjectID `bson:"_id"`
		N  int64         `bson:"n"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.ID.Hex()] = row.N
	}
	return counts, nil
}

// groupCount aggregates one collection by a field into a name->count map. Used
// for records-by-visibility and outbox-by-delivery-state.
func (app *application) groupCount(ctx context.Context, collection, field string) (map[string]int64, error) {
	cursor, err := app.database.Collection(collection).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$" + field}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID string `bson:"_id"`
		N  int64  `bson:"n"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.ID] = row.N
	}
	return counts, nil
}

// ---------------------------------------------------------------------------
// Overview & telemetry
// ---------------------------------------------------------------------------

func (app *application) handleAdminOverview(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	users := app.database.Collection("_users")
	apps := app.database.Collection(appsCollectionName)
	now := time.Now().UTC()

	counts := map[string]int64{}
	counts["builders"], _ = users.CountDocuments(ctx, bson.M{"appId": bson.NilObjectID, "role": builderRole})
	counts["operators"], _ = users.CountDocuments(ctx, bson.M{"appId": bson.NilObjectID, "role": operatorRole})
	counts["apps"], _ = apps.CountDocuments(ctx, bson.M{"sandbox": bson.M{"$ne": true}})
	counts["liveApps"], _ = apps.CountDocuments(ctx, bson.M{"sandbox": bson.M{"$ne": true}, "active": true})
	counts["disabledApps"], _ = apps.CountDocuments(ctx, bson.M{"active": false})
	counts["sandboxes"], _ = apps.CountDocuments(ctx, bson.M{"sandbox": true, "active": true, "expiresAt": bson.M{"$gt": now}})
	counts["endUsers"], _ = users.CountDocuments(ctx, bson.M{"appId": bson.M{"$ne": bson.NilObjectID}})
	counts["records"], _ = app.database.Collection(resourceCollectionName).CountDocuments(ctx, bson.M{})
	counts["sessions"], _ = app.database.Collection(sessionCollectionName).CountDocuments(ctx, bson.M{})
	counts["webhooks"], _ = app.database.Collection(webhookCollectionName).CountDocuments(ctx, bson.M{})

	recordsByVisibility, _ := app.groupCount(ctx, resourceCollectionName, "visibility")
	outboxByState, _ := app.groupCount(ctx, outboxCollectionName, "delivery.state")

	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"counts":              counts,
			"recordsByVisibility": recordsByVisibility,
			"outboxByState":       outboxByState,
			"metrics":             app.metrics.snapshot(),
			"webhookRunning":      app.webhooks != nil && app.webhooks.Enabled(),
		},
	})
}

// ---------------------------------------------------------------------------
// Live settings
// ---------------------------------------------------------------------------

type adminSettingsPatch struct {
	TrySandbox              *bool    `json:"trySandbox"`
	TrySandboxTTL           *string  `json:"trySandboxTTL"`
	BuilderSignupOpen       *bool    `json:"builderSignupOpen"`
	BuilderSignupDailyLimit *int     `json:"builderSignupDailyLimit"`
	DemoSeed                *bool    `json:"demoSeed"`
	WebhookDelivery         *bool    `json:"webhookDelivery"`
	FreeTierMaxApps         *int     `json:"freeTierMaxApps"`
	FreeTierMaxUsers        *int     `json:"freeTierMaxUsers"`
	FreeTierMaxRecords      *int     `json:"freeTierMaxRecords"`
	FreeTierMaxRecordBytes  *int     `json:"freeTierMaxRecordBytes"`
	VibeMaxSandboxes        *int     `json:"vibeMaxSandboxes"`
	VibeDailyLimit          *int     `json:"vibeDailyLimit"`
	VibeShareTTL            *string  `json:"vibeShareTTL"`
	ResetKeys               []string `json:"resetKeys"`
	ResetAll                bool     `json:"resetAll"`
}

func (app *application) handleAdminSettings(w http.ResponseWriter, r *http.Request, _ *Claims) {
	switch r.Method {
	case http.MethodGet:
		app.getAdminSettings(w, r)
	case http.MethodPatch:
		app.patchAdminSettings(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) getAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var overrides settingsOverrides
	if app.settings != nil {
		overrides = app.settings.snapshot(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"effective":   app.effectiveSettings(ctx),
			"envDefaults": resolvedSettingsFromConfig(app.config),
			"overrides":   overrides,
			"environment": app.environmentPosture(),
		},
	})
}

// environmentPosture reports read-only, boot-fixed configuration so the console
// can show the deployment's security posture without exposing any secret value.
func (app *application) environmentPosture() map[string]any {
	return map[string]any{
		"geminiConfigured":        app.vibeEnabled(),
		"geminiModel":             app.config.GeminiModel,
		"vibeCoolingDown":         app.vibeCoolingDown(),
		"metricsTokenConfigured":  app.config.MetricsToken != "",
		"platformTokenConfigured": app.config.PlatformToken != "",
		"operatorConfigured":      app.config.AdminEmail != "",
		"trustProxy":              app.config.TrustProxy,
		"sessionCookieSecure":     app.config.SessionCookieSecure,
		"allowedOrigins":          app.config.AllowedOrigins,
		"publicBaseURL":           app.config.PublicBaseURL,
		"database":                app.config.Database,
		"webhookRunning":          app.webhooks != nil && app.webhooks.Enabled(),
		"githubOAuthConfigured":   app.config.GitHubClientID != "" && app.config.GitHubClientSecret != "",
		"googleOAuthConfigured":   app.config.GoogleClientID != "" && app.config.GoogleClientSecret != "",
	}
}

func (app *application) patchAdminSettings(w http.ResponseWriter, r *http.Request) {
	if app.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "Settings store is unavailable")
		return
	}
	var patch adminSettingsPatch
	if !decodeJSONBody(w, r, &patch, false) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if patch.ResetAll {
		if err := app.settings.resetAll(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "Could not reset settings")
			return
		}
		app.reconcileRuntimeWorkers(ctx)
		app.getAdminSettings(w, r)
		return
	}

	set := bson.M{}
	unset := bson.M{}

	// Resets clear individual overrides back to the .env baseline.
	for _, key := range patch.ResetKeys {
		stored, ok := settingOverrideBSONKeys[key]
		if !ok {
			writeError(w, http.StatusBadRequest, "Unknown setting: "+key)
			return
		}
		unset[stored] = ""
	}

	// Booleans.
	for key, value := range map[string]*bool{
		"trySandbox":        patch.TrySandbox,
		"builderSignupOpen": patch.BuilderSignupOpen,
		"demoSeed":          patch.DemoSeed,
		"webhookDelivery":   patch.WebhookDelivery,
	} {
		if value != nil {
			set[key] = *value
		}
	}

	// Non-negative integer ceilings (0 means unlimited).
	intFields := map[string]*int{
		"builderSignupDailyLimit": patch.BuilderSignupDailyLimit,
		"freeTierMaxApps":         patch.FreeTierMaxApps,
		"freeTierMaxUsers":        patch.FreeTierMaxUsers,
		"freeTierMaxRecords":      patch.FreeTierMaxRecords,
		"freeTierMaxRecordBytes":  patch.FreeTierMaxRecordBytes,
		"vibeMaxSandboxes":        patch.VibeMaxSandboxes,
		"vibeDailyLimit":          patch.VibeDailyLimit,
	}
	for key, value := range intFields {
		if value == nil {
			continue
		}
		if *value < 0 || *value > 100_000_000 {
			writeError(w, http.StatusBadRequest, key+" must be between 0 and 100000000")
			return
		}
		set[key] = *value
	}

	// Durations stored as Go duration strings; validated here so a bad value can
	// never be persisted.
	durationFields := map[string]*string{
		"trySandboxTTL": patch.TrySandboxTTL,
		"vibeShareTTL":  patch.VibeShareTTL,
	}
	for key, value := range durationFields {
		if value == nil {
			continue
		}
		parsed, err := time.ParseDuration(strings.TrimSpace(*value))
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, key+" must be a positive Go duration such as 24h")
			return
		}
		set[key] = parsed.String()
	}

	// A key named in both set and reset is contradictory.
	for key := range set {
		if _, resetToo := unset[key]; resetToo {
			writeError(w, http.StatusBadRequest, "Cannot set and reset the same setting: "+key)
			return
		}
	}

	if err := app.settings.apply(ctx, set, unset); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not update settings")
		return
	}
	app.reconcileRuntimeWorkers(ctx)
	app.getAdminSettings(w, r)
}

// reconcileRuntimeWorkers brings background workers in line with the current
// effective settings after an operator change: it starts or stops webhook
// delivery and re-seeds or clears the demo data on the default app.
func (app *application) reconcileRuntimeWorkers(ctx context.Context) {
	settings := app.effectiveSettings(ctx)
	if app.webhooks != nil {
		app.webhooks.SetEnabled(settings.WebhookDelivery)
	}
	if app.uiAppKey != "" {
		if record, err := app.lookupApp(ctx, app.uiAppKey); err == nil {
			if err := app.syncDemoData(ctx, record.ID); err != nil {
				app.logger.Error("demo data resync after settings change failed", "error", err)
			}
		}
	}
}

func (app *application) handleAdminClearVibeCooldown(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	app.vibeCooldownUntil.Store(0)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"vibeCoolingDown": false}})
}

// ---------------------------------------------------------------------------
// Fleet: apps & sandboxes
// ---------------------------------------------------------------------------

type adminAppView struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	DisplayName    string     `json:"displayName"`
	PublishableKey string     `json:"publishableKey"`
	Active         bool       `json:"active"`
	Sandbox        bool       `json:"sandbox"`
	AllowedOrigins []string   `json:"allowedOrigins,omitempty"`
	OwnerID        string     `json:"ownerId,omitempty"`
	OwnerEmail     string     `json:"ownerEmail,omitempty"`
	OwnerRole      string     `json:"ownerRole,omitempty"`
	Users          int64      `json:"users"`
	Records        int64      `json:"records"`
	Limits         AppLimits  `json:"limits"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	ClaimedAt      *time.Time `json:"claimedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func (app *application) handleAdminApps(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	filter := bson.M{}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("filter"))) {
	case "live":
		filter = bson.M{"sandbox": bson.M{"$ne": true}, "active": true}
	case "sandbox":
		filter = bson.M{"sandbox": true}
	case "disabled":
		filter = bson.M{"active": false}
	}
	if query := strings.TrimSpace(r.URL.Query().Get("q")); query != "" {
		pattern := primitiveRegex(query)
		filter["$or"] = bson.A{
			bson.M{"slug": pattern},
			bson.M{"displayName": pattern},
			bson.M{"publishableKey": pattern},
		}
	}

	cursor, err := app.database.Collection(appsCollectionName).Find(
		ctx, filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(adminListLimit),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list applications")
		return
	}
	var records []AppRecord
	if err := cursor.All(ctx, &records); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list applications")
		return
	}

	userCounts, _ := app.countByAppID(ctx, "_users")
	recordCounts, _ := app.countByAppID(ctx, resourceCollectionName)
	owners := app.ownerDirectory(ctx, records)

	out := make([]adminAppView, 0, len(records))
	for index := range records {
		record := records[index]
		view := adminAppView{
			ID:             record.ID.Hex(),
			Slug:           record.Slug,
			DisplayName:    record.DisplayName,
			PublishableKey: record.PublishableKey,
			Active:         record.Active,
			Sandbox:        record.Sandbox,
			AllowedOrigins: record.AllowedOrigins,
			Users:          userCounts[record.ID.Hex()],
			Records:        recordCounts[record.ID.Hex()],
			Limits:         effectiveLimits(&record),
			ExpiresAt:      record.ExpiresAt,
			ClaimedAt:      record.ClaimedAt,
			CreatedAt:      record.CreatedAt,
			UpdatedAt:      record.UpdatedAt,
		}
		if !record.OwnerID.IsZero() {
			view.OwnerID = record.OwnerID.Hex()
			if owner, ok := owners[record.OwnerID.Hex()]; ok {
				view.OwnerEmail = owner.Email
				view.OwnerRole = owner.Role
			}
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// ownerDirectory resolves the owner email/role for a set of app records in one
// query, so the fleet list never issues a lookup per app.
func (app *application) ownerDirectory(ctx context.Context, records []AppRecord) map[string]User {
	ids := make([]bson.ObjectID, 0, len(records))
	seen := map[string]struct{}{}
	for _, record := range records {
		if record.OwnerID.IsZero() {
			continue
		}
		if _, ok := seen[record.OwnerID.Hex()]; ok {
			continue
		}
		seen[record.OwnerID.Hex()] = struct{}{}
		ids = append(ids, record.OwnerID)
	}
	directory := map[string]User{}
	if len(ids) == 0 {
		return directory
	}
	cursor, err := app.database.Collection("_users").Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return directory
	}
	var owners []User
	if err := cursor.All(ctx, &owners); err != nil {
		return directory
	}
	for _, owner := range owners {
		directory[owner.ID.Hex()] = owner
	}
	return directory
}

func (app *application) handleAdminApp(w http.ResponseWriter, r *http.Request, _ *Claims) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/apps/"), "/")
	segments := strings.Split(rest, "/")
	appID, err := bson.ObjectIDFromHex(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid application id")
		return
	}
	switch {
	case len(segments) == 1 && r.Method == http.MethodPatch:
		app.updateAdminApp(w, r, appID)
	case len(segments) == 1 && r.Method == http.MethodDelete:
		app.deleteAdminApp(w, r, appID)
	case len(segments) == 2 && segments[1] == "users" && r.Method == http.MethodGet:
		app.adminListAppUsers(w, r, appID)
	case len(segments) == 3 && segments[1] == "users" && r.Method == http.MethodDelete:
		userID, err := bson.ObjectIDFromHex(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid user id")
			return
		}
		app.adminDeleteAppUser(w, r, appID, userID)
	case len(segments) == 2 && segments[1] == "resources" && r.Method == http.MethodGet:
		app.adminListAppResources(w, r, appID)
	case len(segments) == 3 && segments[1] == "resources" && r.Method == http.MethodDelete:
		resourceID, err := bson.ObjectIDFromHex(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid record id")
			return
		}
		app.adminDeleteAppResource(w, r, appID, resourceID)
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

type adminAppPatch struct {
	Active         *bool      `json:"active"`
	DisplayName    *string    `json:"displayName"`
	AllowedOrigins *[]string  `json:"allowedOrigins"`
	Limits         *AppLimits `json:"limits"`
	Unlimited      *bool      `json:"unlimited"`
	ExtendHours    *int       `json:"extendHours"`
	OwnerID        *string    `json:"ownerId"`
	Promote        *bool      `json:"promote"`
}

func (app *application) updateAdminApp(w http.ResponseWriter, r *http.Request, appID bson.ObjectID) {
	var patch adminAppPatch
	if !decodeJSONBody(w, r, &patch, false) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	record, err := app.appExists(ctx, appID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not update application")
		return
	}

	set := bson.M{"updatedAt": time.Now().UTC()}
	unset := bson.M{}

	if patch.Active != nil {
		set["active"] = *patch.Active
	}
	if patch.DisplayName != nil {
		name := strings.TrimSpace(*patch.DisplayName)
		if name == "" || len(name) > 80 {
			writeError(w, http.StatusBadRequest, "displayName must be 1 to 80 characters")
			return
		}
		set["displayName"] = name
	}
	if patch.AllowedOrigins != nil {
		origins, err := normalizeOrigins(*patch.AllowedOrigins)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		set["allowedOrigins"] = origins
	}
	if patch.Unlimited != nil && *patch.Unlimited {
		unset["limits"] = ""
	} else if patch.Limits != nil {
		if patch.Limits.MaxUsers < 0 || patch.Limits.MaxRecordsPerNamespace < 0 || patch.Limits.MaxRecordBytes < 0 {
			writeError(w, http.StatusBadRequest, "limits must be non-negative (0 means unlimited)")
			return
		}
		set["limits"] = *patch.Limits
	}
	if patch.ExtendHours != nil {
		if !record.Sandbox {
			writeError(w, http.StatusBadRequest, "Only sandbox apps have an expiry to extend")
			return
		}
		if *patch.ExtendHours < 1 || *patch.ExtendHours > 120 {
			writeError(w, http.StatusBadRequest, "extendHours must be between 1 and 120")
			return
		}
		base := time.Now().UTC()
		if record.ExpiresAt != nil && record.ExpiresAt.After(base) {
			base = *record.ExpiresAt
		}
		set["expiresAt"] = base.Add(time.Duration(*patch.ExtendHours) * time.Hour)
	}
	if patch.Promote != nil && *patch.Promote {
		set["sandbox"] = false
		unset["expiresAt"] = ""
		unset["claimTokenHash"] = ""
	}
	if patch.OwnerID != nil {
		ownerID, err := bson.ObjectIDFromHex(strings.TrimSpace(*patch.OwnerID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid owner id")
			return
		}
		count, err := app.database.Collection("_users").CountDocuments(ctx, bson.M{"_id": ownerID, "appId": bson.NilObjectID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not verify owner")
			return
		}
		if count == 0 {
			writeError(w, http.StatusBadRequest, "Owner must be an existing builder or operator")
			return
		}
		set["ownerId"] = ownerID
	}

	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	var updated AppRecord
	err = app.database.Collection(appsCollectionName).FindOneAndUpdate(
		ctx, bson.M{"_id": appID}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update application")
		return
	}
	app.appCache.delete(updated.PublishableKey)
	writeJSON(w, http.StatusOK, map[string]any{"data": app.adminAppViewFor(ctx, updated)})
}

// adminAppViewFor builds a single app view with counts and owner info.
func (app *application) adminAppViewFor(ctx context.Context, record AppRecord) adminAppView {
	view := adminAppView{
		ID:             record.ID.Hex(),
		Slug:           record.Slug,
		DisplayName:    record.DisplayName,
		PublishableKey: record.PublishableKey,
		Active:         record.Active,
		Sandbox:        record.Sandbox,
		AllowedOrigins: record.AllowedOrigins,
		Limits:         effectiveLimits(&record),
		ExpiresAt:      record.ExpiresAt,
		ClaimedAt:      record.ClaimedAt,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}
	view.Users, _ = app.database.Collection("_users").CountDocuments(ctx, bson.M{"appId": record.ID})
	view.Records, _ = app.database.Collection(resourceCollectionName).CountDocuments(ctx, bson.M{"appId": record.ID})
	if !record.OwnerID.IsZero() {
		view.OwnerID = record.OwnerID.Hex()
		var owner User
		if err := app.database.Collection("_users").FindOne(ctx, bson.M{"_id": record.OwnerID}).Decode(&owner); err == nil {
			view.OwnerEmail = owner.Email
			view.OwnerRole = owner.Role
		}
	}
	return view
}

func (app *application) deleteAdminApp(w http.ResponseWriter, r *http.Request, appID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	record, err := app.appExists(ctx, appID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}
	if _, err := app.database.Collection(appsCollectionName).
		UpdateOne(ctx, bson.M{"_id": appID}, bson.M{"$set": bson.M{"active": false}}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}
	app.appCache.delete(record.PublishableKey)
	if err := app.purgeAppTenantData(ctx, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application data")
		return
	}
	if _, err := app.database.Collection(appsCollectionName).DeleteOne(ctx, bson.M{"_id": appID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete application")
		return
	}
	app.logger.Info("operator deleted app", "app", appID.Hex())
	w.WriteHeader(http.StatusNoContent)
}

func (app *application) adminListAppUsers(w http.ResponseWriter, r *http.Request, appID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.appExists(ctx, appID); err != nil {
		app.writeAdminAppLookupError(w, err, "Could not read users")
		return
	}
	users, err := app.inspectAppUsers(ctx, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read users")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": users})
}

func (app *application) adminDeleteAppUser(w http.ResponseWriter, r *http.Request, appID, userID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.appExists(ctx, appID); err != nil {
		app.writeAdminAppLookupError(w, err, "Could not delete user")
		return
	}
	found, err := app.deleteAppUserCascade(ctx, appID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete user")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (app *application) adminListAppResources(w http.ResponseWriter, r *http.Request, appID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.appExists(ctx, appID); err != nil {
		app.writeAdminAppLookupError(w, err, "Could not read records")
		return
	}
	records, err := app.inspectAppResources(ctx, appID, r.URL.Query().Get("type"), r.URL.Query().Get("visibility"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read records")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": records})
}

func (app *application) adminDeleteAppResource(w http.ResponseWriter, r *http.Request, appID, resourceID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := app.appExists(ctx, appID); err != nil {
		app.writeAdminAppLookupError(w, err, "Could not delete record")
		return
	}
	result, err := app.database.Collection(resourceCollectionName).DeleteOne(ctx, bson.M{"_id": resourceID, "appId": appID})
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

func (app *application) writeAdminAppLookupError(w http.ResponseWriter, err error, serverMessage string) {
	if errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	writeError(w, http.StatusInternalServerError, serverMessage)
}

func (app *application) handleAdminSweep(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	purged, err := app.purgeExpiredSandboxes(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not sweep sandboxes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"purged": purged}})
}

// purgeExpiredSandboxes removes every expired sandbox tenant on demand and
// reports how many were purged. It shares purgeSandboxApp with the sweeper.
func (app *application) purgeExpiredSandboxes(ctx context.Context) (int, error) {
	cursor, err := app.database.Collection(appsCollectionName).Find(ctx, bson.M{
		"sandbox":   true,
		"expiresAt": bson.M{"$lte": time.Now().UTC()},
	})
	if err != nil {
		return 0, err
	}
	var expired []AppRecord
	if err := cursor.All(ctx, &expired); err != nil {
		return 0, err
	}
	purged := 0
	for _, record := range expired {
		if err := app.purgeSandboxApp(ctx, record); err != nil {
			app.logger.Error("operator sandbox sweep purge failed", "error", err, "app", record.ID.Hex())
			continue
		}
		purged++
	}
	return purged, nil
}

// ---------------------------------------------------------------------------
// Builders directory
// ---------------------------------------------------------------------------

type adminBuilderView struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	Role             string    `json:"role"`
	Plan             string    `json:"plan,omitempty"`
	AppCount         int64     `json:"appCount"`
	IsBootstrapAdmin bool      `json:"isBootstrapAdmin"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (app *application) handleAdminBuilders(w http.ResponseWriter, r *http.Request, _ *Claims) {
	switch r.Method {
	case http.MethodGet:
		app.listAdminBuilders(w, r)
	case http.MethodPost:
		app.createAdminBuilder(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (app *application) listAdminBuilders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	cursor, err := app.database.Collection("_users").Find(
		ctx,
		bson.M{"appId": bson.NilObjectID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(adminListLimit),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list builders")
		return
	}
	var principals []User
	if err := cursor.All(ctx, &principals); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list builders")
		return
	}
	appCounts, _ := app.appCountByOwner(ctx)
	bootstrapEmail := normalizeEmail(app.config.AdminEmail)
	out := make([]adminBuilderView, 0, len(principals))
	for _, principal := range principals {
		out = append(out, adminBuilderView{
			ID:               principal.ID.Hex(),
			Email:            principal.Email,
			Role:             principal.Role,
			Plan:             principal.Plan,
			AppCount:         appCounts[principal.ID.Hex()],
			IsBootstrapAdmin: bootstrapEmail != "" && principal.Email == bootstrapEmail,
			CreatedAt:        principal.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

type adminBuilderCreate struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Plan     string `json:"plan"`
}

func (app *application) createAdminBuilder(w http.ResponseWriter, r *http.Request) {
	var input adminBuilderCreate
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	input.Email = normalizeEmail(input.Email)
	if err := validateSignUpCredentials(input.Email, input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := builderRole
	if input.Role == operatorRole {
		role = operatorRole
	} else if input.Role != "" && input.Role != builderRole {
		writeError(w, http.StatusBadRequest, "role must be builder or operator")
		return
	}
	plan := ""
	if input.Plan == freePlan {
		plan = freePlan
	} else if input.Plan != "" && input.Plan != "unlimited" {
		writeError(w, http.StatusBadRequest, "plan must be free or unlimited")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error hashing password")
		return
	}
	principal := User{
		ID:           bson.NewObjectID(),
		AppID:        bson.NilObjectID,
		Email:        input.Email,
		PasswordHash: string(passwordHash),
		Role:         role,
		Plan:         plan,
		CreatedAt:    time.Now().UTC(),
	}
	if _, err := app.database.Collection("_users").InsertOne(ctx, principal); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeError(w, http.StatusConflict, "A builder with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not create builder")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": adminBuilderView{
		ID:        principal.ID.Hex(),
		Email:     principal.Email,
		Role:      principal.Role,
		Plan:      principal.Plan,
		CreatedAt: principal.CreatedAt,
	}})
}

func (app *application) handleAdminBuilder(w http.ResponseWriter, r *http.Request, claims *Claims) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/builders/"), "/")
	builderID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid builder id")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		app.updateAdminBuilder(w, r, claims, builderID)
	case http.MethodDelete:
		app.deleteAdminBuilder(w, r, claims, builderID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

type adminBuilderPatch struct {
	Plan              *string `json:"plan"`
	Role              *string `json:"role"`
	Password          *string `json:"password"`
	ApplyLimitsToApps *bool   `json:"applyLimitsToApps"`
}

func (app *application) updateAdminBuilder(w http.ResponseWriter, r *http.Request, claims *Claims, builderID bson.ObjectID) {
	var patch adminBuilderPatch
	if !decodeJSONBody(w, r, &patch, false) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var target User
	if err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"_id": builderID, "appId": bson.NilObjectID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}}).Decode(&target); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Builder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not update builder")
		return
	}
	isBootstrap := normalizeEmail(app.config.AdminEmail) != "" && target.Email == normalizeEmail(app.config.AdminEmail)
	isSelf := claims.UID == builderID.Hex()

	set := bson.M{}
	unset := bson.M{}

	if patch.Role != nil {
		role := *patch.Role
		if role != builderRole && role != operatorRole {
			writeError(w, http.StatusBadRequest, "role must be builder or operator")
			return
		}
		if role != operatorRole && (isBootstrap || isSelf) {
			writeError(w, http.StatusForbidden, "You cannot remove operator access from the bootstrap operator or yourself")
			return
		}
		set["role"] = role
	}
	if patch.Plan != nil {
		switch *patch.Plan {
		case freePlan:
			set["plan"] = freePlan
			if patch.ApplyLimitsToApps != nil && *patch.ApplyLimitsToApps {
				limits := freeTierLimitsFromSettings(app.effectiveSettings(ctx))
				if _, err := app.database.Collection(appsCollectionName).UpdateMany(ctx, bson.M{"ownerId": builderID}, bson.M{"$set": bson.M{"limits": limits}}); err != nil {
					writeError(w, http.StatusInternalServerError, "Could not apply limits to owned apps")
					return
				}
				app.evictOwnerAppCache(ctx, builderID)
			}
		case "unlimited":
			unset["plan"] = ""
			if patch.ApplyLimitsToApps != nil && *patch.ApplyLimitsToApps {
				if _, err := app.database.Collection(appsCollectionName).UpdateMany(ctx, bson.M{"ownerId": builderID}, bson.M{"$unset": bson.M{"limits": ""}}); err != nil {
					writeError(w, http.StatusInternalServerError, "Could not lift limits on owned apps")
					return
				}
				app.evictOwnerAppCache(ctx, builderID)
			}
		default:
			writeError(w, http.StatusBadRequest, "plan must be free or unlimited")
			return
		}
	}
	if patch.Password != nil {
		if err := validateSignUpCredentials(target.Email, *patch.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(*patch.Password), bcryptCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Error hashing password")
			return
		}
		set["password"] = string(passwordHash)
		// Invalidate existing browser sessions so a reset password takes hold.
		if _, err := app.database.Collection(sessionCollectionName).
			UpdateMany(ctx, bson.M{"userId": builderID, "revokedAt": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"revokedAt": time.Now().UTC()}}); err != nil {
			app.logger.Error("could not revoke sessions after password reset", "error", err)
		}
	}

	update := bson.M{}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if len(update) > 0 {
		if _, err := app.database.Collection("_users").UpdateOne(ctx, bson.M{"_id": builderID}, update); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not update builder")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": builderID.Hex()}})
}

// evictOwnerAppCache drops cached records for an owner's apps so a limits change
// is honored on the next request instead of after the cache TTL.
func (app *application) evictOwnerAppCache(ctx context.Context, ownerID bson.ObjectID) {
	cursor, err := app.database.Collection(appsCollectionName).Find(ctx, bson.M{"ownerId": ownerID})
	if err != nil {
		return
	}
	var records []AppRecord
	if err := cursor.All(ctx, &records); err != nil {
		return
	}
	for _, record := range records {
		app.appCache.delete(record.PublishableKey)
	}
}

func (app *application) deleteAdminBuilder(w http.ResponseWriter, r *http.Request, claims *Claims, builderID bson.ObjectID) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var target User
	if err := app.database.Collection("_users").
		FindOne(ctx, bson.M{"_id": builderID, "appId": bson.NilObjectID, "role": bson.M{"$in": bson.A{builderRole, operatorRole}}}).Decode(&target); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Builder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not delete builder")
		return
	}
	if claims.UID == builderID.Hex() {
		writeError(w, http.StatusForbidden, "You cannot delete your own account")
		return
	}
	if normalizeEmail(app.config.AdminEmail) != "" && target.Email == normalizeEmail(app.config.AdminEmail) {
		writeError(w, http.StatusForbidden, "The bootstrap operator cannot be deleted; remove ADMIN_EMAIL/ADMIN_PASSWORD and restart instead")
		return
	}

	// Cascade: purge and delete every app this builder owns.
	cursor, err := app.database.Collection(appsCollectionName).Find(ctx, bson.M{"ownerId": builderID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete builder")
		return
	}
	var owned []AppRecord
	if err := cursor.All(ctx, &owned); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete builder")
		return
	}
	for _, record := range owned {
		app.appCache.delete(record.PublishableKey)
		if err := app.purgeAppTenantData(ctx, record.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not delete builder's app data")
			return
		}
		if _, err := app.database.Collection(appsCollectionName).DeleteOne(ctx, bson.M{"_id": record.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not delete builder's app")
			return
		}
	}
	// Revoke sessions and remove the account.
	if _, err := app.database.Collection(sessionCollectionName).DeleteMany(ctx, bson.M{"userId": builderID}); err != nil {
		app.logger.Error("could not delete builder sessions", "error", err)
	}
	if _, err := app.database.Collection("_users").DeleteOne(ctx, bson.M{"_id": builderID}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete builder")
		return
	}
	app.logger.Info("operator deleted builder", "builder", builderID.Hex(), "ownedApps", len(owned))
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Outbox & tinkering
// ---------------------------------------------------------------------------

func (app *application) handleAdminOutbox(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	filter := bson.M{}
	if state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state"))); state != "" {
		filter["delivery.state"] = state
	}
	cursor, err := app.database.Collection(outboxCollectionName).Find(
		ctx, filter,
		options.Find().SetSort(bson.D{{Key: "occurredAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(adminListLimit),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list outbox events")
		return
	}
	events := make([]outboxEvent, 0)
	if err := cursor.All(ctx, &events); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list outbox events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

func (app *application) handleAdminOutboxItem(w http.ResponseWriter, r *http.Request, _ *Claims) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/outbox/"), "/")
	segments := strings.Split(rest, "/")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	switch {
	case len(segments) == 1 && segments[0] == "retry-dead" && r.Method == http.MethodPost:
		app.retryDeadOutbox(ctx, w)
	case len(segments) == 1 && segments[0] == "delivered" && r.Method == http.MethodDelete:
		app.pruneDeliveredOutbox(ctx, w)
	case len(segments) == 2 && segments[1] == "retry" && r.Method == http.MethodPost:
		eventID, err := bson.ObjectIDFromHex(segments[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid event id")
			return
		}
		app.retryOutboxEvent(ctx, w, eventID)
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

// outboxRetryUpdate resets an event to pending for immediate redelivery. It also
// clears delivery.expiresAt so a previously delivered event that is being retried
// is not reaped by the TTL index while it waits to be redelivered.
func outboxRetryUpdate() bson.M {
	return bson.M{
		"$set": bson.M{
			"delivery.state":       "pending",
			"delivery.attempts":    0,
			"delivery.availableAt": time.Now().UTC(),
		},
		"$unset": bson.M{"delivery.expiresAt": ""},
	}
}

func (app *application) retryOutboxEvent(ctx context.Context, w http.ResponseWriter, eventID bson.ObjectID) {
	result, err := app.database.Collection(outboxCollectionName).UpdateOne(ctx, bson.M{"_id": eventID}, outboxRetryUpdate())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not retry event")
		return
	}
	if result.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "Event not found")
		return
	}
	if app.webhooks != nil {
		app.webhooks.TriggerDrain()
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"retried": 1}})
}

func (app *application) retryDeadOutbox(ctx context.Context, w http.ResponseWriter) {
	result, err := app.database.Collection(outboxCollectionName).UpdateMany(ctx, bson.M{"delivery.state": "dead"}, outboxRetryUpdate())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not retry dead-lettered events")
		return
	}
	if app.webhooks != nil {
		app.webhooks.TriggerDrain()
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"retried": result.ModifiedCount}})
}

func (app *application) pruneDeliveredOutbox(ctx context.Context, w http.ResponseWriter) {
	result, err := app.database.Collection(outboxCollectionName).DeleteMany(ctx, bson.M{"delivery.state": "delivered"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not prune delivered events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"pruned": result.DeletedCount}})
}

func (app *application) handleAdminTinkerSandbox(w http.ResponseWriter, r *http.Request, _ *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ttl := app.effectiveSettings(ctx).TrySandboxTTL
	provision, err := app.provisionSandbox(ctx, sandboxOptions{DisplayName: "Operator tinker", TTL: ttl})
	if err != nil {
		if errors.Is(err, errVibeSandboxFull) {
			writeError(w, http.StatusServiceUnavailable, "The sandbox pool is full right now")
			return
		}
		app.logger.Error("operator tinker sandbox provisioning failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Could not start a sandbox")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"publishableKey": provision.record.PublishableKey,
		"expiresAt":      provision.record.ExpiresAt,
		"apiBase":        app.publicBaseURL(r),
		"claim":          map[string]any{"token": provision.claimToken},
	})
}

// primitiveRegex builds a case-insensitive, anchored-free regex match for a
// search term with its metacharacters escaped so user input can't alter it.
func primitiveRegex(term string) bson.M {
	return bson.M{"$regex": regexp.QuoteMeta(term), "$options": "i"}
}

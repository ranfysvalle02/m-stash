package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"golang.org/x/crypto/bcrypt"
)

const appsCollectionName = "_m_stash_apps"

const (
	publishableKeyPrefix = "pk_"
	secretKeyPrefix      = "sk_"
	appKeyHeader         = "X-M-Stash-App"
)

const appContextKey contextKey = "app-record"

// AppRecord is one tenant: an isolated application sharing the deployment.
type AppRecord struct {
	ID             bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Slug           string        `bson:"slug" json:"slug"`
	DisplayName    string        `bson:"displayName" json:"displayName"`
	PublishableKey string        `bson:"publishableKey" json:"publishableKey"`
	SecretKeyHash  string        `bson:"secretKeyHash" json:"-"`
	AllowedOrigins []string      `bson:"allowedOrigins,omitempty" json:"allowedOrigins,omitempty"`
	Active         bool          `bson:"active" json:"active"`
	CreatedAt      time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt      time.Time     `bson:"updatedAt" json:"updatedAt"`
}

func withApp(ctx context.Context, record *AppRecord) context.Context {
	return context.WithValue(ctx, appContextKey, record)
}

func appFromContext(ctx context.Context) (*AppRecord, bool) {
	record, ok := ctx.Value(appContextKey).(*AppRecord)
	return record, ok
}

func appIDFromContext(ctx context.Context) (bson.ObjectID, bool) {
	record, ok := appFromContext(ctx)
	if !ok {
		return bson.NilObjectID, false
	}
	return record.ID, true
}

func appIDHexFromContext(ctx context.Context) string {
	if appID, ok := appIDFromContext(ctx); ok {
		return appID.Hex()
	}
	return ""
}

func generateAppKey(prefix string) (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func ensureAppIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(appsCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "publishableKey", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
	})
	return err
}

// appKeyCache memoizes publishable-key lookups so hot paths avoid a Mongo round trip per request.
type appKeyCache struct {
	mu      sync.RWMutex
	entries map[string]appCacheEntry
	ttl     time.Duration
}

type appCacheEntry struct {
	record    *AppRecord
	expiresAt time.Time
}

func newAppKeyCache(ttl time.Duration) *appKeyCache {
	return &appKeyCache{entries: make(map[string]appCacheEntry), ttl: ttl}
}

func (cache *appKeyCache) get(key string) (*AppRecord, bool) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, ok := cache.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.record, true
}

func (cache *appKeyCache) put(key string, record *AppRecord) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries[key] = appCacheEntry{record: record, expiresAt: time.Now().Add(cache.ttl)}
}

func (app *application) lookupApp(ctx context.Context, publishableKey string) (*AppRecord, error) {
	if record, ok := app.appCache.get(publishableKey); ok {
		return record, nil
	}
	var record AppRecord
	if err := app.database.Collection(appsCollectionName).FindOne(
		ctx,
		bson.M{"publishableKey": publishableKey, "active": true},
	).Decode(&record); err != nil {
		return nil, err
	}
	app.appCache.put(publishableKey, &record)
	return &record, nil
}

// ensureDefaultApp resolves the publishable key the bundled UI ships with, so a
// fresh deployment works without any manual provisioning. An explicit UI_APP_KEY
// always wins; otherwise the default tenant is found-or-created on boot.
func (app *application) ensureDefaultApp(ctx context.Context) (string, error) {
	if key := strings.TrimSpace(app.config.UIAppKey); key != "" {
		return key, nil
	}

	slug := normalizeNamespaceSlug(app.config.DefaultAppSlug)
	if slug == "" {
		slug = "m-stash"
	}

	var existing AppRecord
	err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"slug": slug}).Decode(&existing)
	if err == nil {
		return existing.PublishableKey, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return "", err
	}

	// Adopt any existing app rather than creating a duplicate tenant.
	err = app.database.Collection(appsCollectionName).FindOne(
		ctx,
		bson.M{"active": true},
		options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: 1}}),
	).Decode(&existing)
	if err == nil {
		return existing.PublishableKey, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return "", err
	}

	record, _, _, provisionErr := app.provisionApp(ctx, provisionAppInput{
		Slug:        slug,
		DisplayName: app.config.DefaultAppName,
	})
	if provisionErr != nil {
		// Another instance may have won the race; fall back to a lookup.
		if mongo.IsDuplicateKeyError(provisionErr) {
			if lookupErr := app.database.Collection(appsCollectionName).
				FindOne(ctx, bson.M{"slug": slug}).Decode(&existing); lookupErr == nil {
				return existing.PublishableKey, nil
			}
		}
		return "", provisionErr
	}
	return record.PublishableKey, nil
}

func appKeyFromRequest(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get(appKeyHeader)); key != "" {
		return key
	}
	return strings.TrimSpace(r.URL.Query().Get("app"))
}

// resolveApp binds the request to its tenant from the publishable key before any app-scoped handler runs.
func (app *application) resolveApp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := appKeyFromRequest(r)
		if key == "" {
			if r.Method == http.MethodOptions {
				next(w, r)
				return
			}
			writeError(w, http.StatusBadRequest, "Missing "+appKeyHeader+" application key")
			return
		}
		record, err := app.lookupApp(r.Context(), key)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeError(w, http.StatusUnauthorized, "Unknown application key")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "Could not resolve application")
			return
		}
		next(w, r.WithContext(withApp(r.Context(), record)))
	}
}

func (app *application) requirePlatformToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !app.matchesPlatformToken(r.Header.Get("Authorization")) {
			writeError(w, http.StatusUnauthorized, "Invalid platform token")
			return
		}
		next(w, r)
	}
}

func (app *application) matchesPlatformToken(authorization string) bool {
	if app.config.PlatformToken == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	return constantTimeStringEqual(token, app.config.PlatformToken)
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// matchesAppSecret authenticates a backend caller's secret key against the resolved tenant.
func (app *application) matchesAppSecret(ctx context.Context, authorization string) bool {
	record, ok := appFromContext(ctx)
	if !ok || record.SecretKeyHash == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	if !strings.HasPrefix(token, secretKeyPrefix) {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(record.SecretKeyHash), []byte(token)) == nil
}

type provisionAppInput struct {
	Slug           string   `json:"slug"`
	DisplayName    string   `json:"displayName"`
	AdminEmail     string   `json:"adminEmail"`
	AdminPassword  string   `json:"adminPassword"`
	AllowedOrigins []string `json:"allowedOrigins"`
}

func (input *provisionAppInput) normalizeAndValidate() error {
	input.Slug = normalizeNamespaceSlug(input.Slug)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if err := validateNamespaceSlug(input.Slug); err != nil {
		return errors.New("slug must be a valid 3-32 character application identifier")
	}
	if input.DisplayName == "" {
		input.DisplayName = input.Slug
	}
	if len(input.DisplayName) > 80 {
		return errors.New("displayName must be 80 characters or fewer")
	}
	if (input.AdminEmail == "") != (input.AdminPassword == "") {
		return errors.New("adminEmail and adminPassword must be provided together")
	}
	if input.AdminEmail != "" {
		normalizedEmail, err := validateBootstrapAdminCredentials(input.AdminEmail, input.AdminPassword)
		if err != nil {
			return err
		}
		input.AdminEmail = normalizedEmail
	}
	cleanOrigins := make([]string, 0, len(input.AllowedOrigins))
	for _, origin := range input.AllowedOrigins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			cleanOrigins = append(cleanOrigins, trimmed)
		}
	}
	input.AllowedOrigins = cleanOrigins
	return nil
}

func (app *application) handleApps(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var input provisionAppInput
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	if err := input.normalizeAndValidate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

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

func (app *application) provisionApp(ctx context.Context, input provisionAppInput) (AppRecord, string, *User, error) {
	publishableKey, err := generateAppKey(publishableKeyPrefix)
	if err != nil {
		return AppRecord{}, "", nil, err
	}
	secretKey, err := generateAppKey(secretKeyPrefix)
	if err != nil {
		return AppRecord{}, "", nil, err
	}
	secretHash, err := bcrypt.GenerateFromPassword([]byte(secretKey), bcryptCost)
	if err != nil {
		return AppRecord{}, "", nil, err
	}
	now := time.Now().UTC()
	record := AppRecord{
		ID:             bson.NewObjectID(),
		Slug:           input.Slug,
		DisplayName:    input.DisplayName,
		PublishableKey: publishableKey,
		SecretKeyHash:  string(secretHash),
		AllowedOrigins: input.AllowedOrigins,
		Active:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	var admin *User

	session, err := app.mongoClient.StartSession()
	if err != nil {
		return AppRecord{}, "", nil, err
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, err := app.database.Collection(appsCollectionName).InsertOne(transactionContext, record); err != nil {
			return nil, err
		}
		sharedNamespace := newSharedNamespace(record.ID, now)
		if _, err := app.database.Collection(namespaceCollectionName).InsertOne(transactionContext, sharedNamespace); err != nil {
			return nil, err
		}
		if input.AdminEmail != "" {
			passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.AdminPassword), bcryptCost)
			if err != nil {
				return nil, err
			}
			adminUser := User{
				ID:           bson.NewObjectID(),
				AppID:        record.ID,
				Email:        input.AdminEmail,
				PasswordHash: string(passwordHash),
				Role:         "admin",
				CreatedAt:    now,
			}
			if _, err := app.database.Collection("_users").InsertOne(transactionContext, adminUser); err != nil {
				return nil, err
			}
			admin = &adminUser
		}
		return nil, nil
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return AppRecord{}, "", nil, err
	}
	return record, secretKey, admin, nil
}

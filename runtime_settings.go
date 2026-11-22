package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	runtimeSettingsCollection = "_m_stash_settings"
	runtimeSettingsDocID      = "deployment"
	// runtimeSettingsRefreshTTL bounds how stale a replica's cached overrides can
	// be, so an operator's change propagates across every instance within seconds
	// without a per-request Mongo read.
	runtimeSettingsRefreshTTL = 5 * time.Second
)

// settingsOverrides holds only the fields an operator has explicitly overridden
// from the .env baseline. A nil pointer means "use the .env value". Durations
// are stored as Go duration strings so the document stays human-readable.
type settingsOverrides struct {
	TrySandbox              *bool   `bson:"trySandbox,omitempty" json:"trySandbox,omitempty"`
	TrySandboxTTL           *string `bson:"trySandboxTTL,omitempty" json:"trySandboxTTL,omitempty"`
	BuilderSignupOpen       *bool   `bson:"builderSignupOpen,omitempty" json:"builderSignupOpen,omitempty"`
	BuilderSignupDailyLimit *int    `bson:"builderSignupDailyLimit,omitempty" json:"builderSignupDailyLimit,omitempty"`
	DemoSeed                *bool   `bson:"demoSeed,omitempty" json:"demoSeed,omitempty"`
	WebhookDelivery         *bool   `bson:"webhookDelivery,omitempty" json:"webhookDelivery,omitempty"`
	FreeTierMaxApps         *int    `bson:"freeTierMaxApps,omitempty" json:"freeTierMaxApps,omitempty"`
	FreeTierMaxUsers        *int    `bson:"freeTierMaxUsers,omitempty" json:"freeTierMaxUsers,omitempty"`
	FreeTierMaxRecords      *int    `bson:"freeTierMaxRecords,omitempty" json:"freeTierMaxRecords,omitempty"`
	FreeTierMaxRecordBytes  *int    `bson:"freeTierMaxRecordBytes,omitempty" json:"freeTierMaxRecordBytes,omitempty"`
	VibeMaxSandboxes        *int    `bson:"vibeMaxSandboxes,omitempty" json:"vibeMaxSandboxes,omitempty"`
	VibeDailyLimit          *int    `bson:"vibeDailyLimit,omitempty" json:"vibeDailyLimit,omitempty"`
	VibeShareTTL            *string `bson:"vibeShareTTL,omitempty" json:"vibeShareTTL,omitempty"`
}

// resolvedSettings is the effective configuration a request should honor right
// now: the .env baseline with any operator overrides applied and all the same
// ceilings the boot path enforces.
type resolvedSettings struct {
	TrySandbox              bool          `json:"trySandbox"`
	TrySandboxTTL           time.Duration `json:"-"`
	BuilderSignupOpen       bool          `json:"builderSignupOpen"`
	BuilderSignupDailyLimit int           `json:"builderSignupDailyLimit"`
	DemoSeed                bool          `json:"demoSeed"`
	WebhookDelivery         bool          `json:"webhookDelivery"`
	FreeTierMaxApps         int           `json:"freeTierMaxApps"`
	FreeTierMaxUsers        int           `json:"freeTierMaxUsers"`
	FreeTierMaxRecords      int           `json:"freeTierMaxRecords"`
	FreeTierMaxRecordBytes  int           `json:"freeTierMaxRecordBytes"`
	VibeMaxSandboxes        int           `json:"vibeMaxSandboxes"`
	VibeDailyLimit          int           `json:"vibeDailyLimit"`
	VibeShareTTL            time.Duration `json:"-"`
	// String mirrors of the durations so the admin API can serialize them.
	TrySandboxTTLString string `json:"trySandboxTTL"`
	VibeShareTTLString  string `json:"vibeShareTTL"`
}

// runtimeSettingsStore persists operator overrides in a single MongoDB document
// and memoizes them behind an RWMutex so reads are cheap and writes take effect
// immediately on the writing replica (and within the refresh TTL elsewhere).
type runtimeSettingsStore struct {
	collection *mongo.Collection
	config     Config

	mu         sync.RWMutex
	overrides  settingsOverrides
	loaded     bool
	lastLoaded time.Time
}

func newRuntimeSettingsStore(database *mongo.Database, config Config) *runtimeSettingsStore {
	return &runtimeSettingsStore{
		collection: database.Collection(runtimeSettingsCollection),
		config:     config,
	}
}

// currentOverrides returns the cached overrides, refreshing from Mongo when the
// cache is cold or older than the refresh TTL.
func (store *runtimeSettingsStore) currentOverrides(ctx context.Context) settingsOverrides {
	store.mu.RLock()
	fresh := store.loaded && time.Since(store.lastLoaded) < runtimeSettingsRefreshTTL
	overrides := store.overrides
	store.mu.RUnlock()
	if fresh {
		return overrides
	}
	return store.refresh(ctx)
}

// refresh reloads the overrides document, replacing the cache. A transient read
// error keeps the last good cache so a brief Mongo blip never flips settings.
func (store *runtimeSettingsStore) refresh(ctx context.Context) settingsOverrides {
	var doc settingsOverrides
	err := store.collection.FindOne(ctx, bson.M{"_id": runtimeSettingsDocID}).Decode(&doc)
	store.mu.Lock()
	defer store.mu.Unlock()
	switch {
	case err == nil:
		store.overrides = doc
		store.loaded = true
	case errors.Is(err, mongo.ErrNoDocuments):
		store.overrides = settingsOverrides{}
		store.loaded = true
	default:
		// Keep the previous cache but back off so we don't hammer a struggling
		// database on every request.
		store.lastLoaded = time.Now()
		return store.overrides
	}
	store.lastLoaded = time.Now()
	return store.overrides
}

// snapshot returns the overrides as currently cached (refreshing if stale), for
// the admin settings view.
func (store *runtimeSettingsStore) snapshot(ctx context.Context) settingsOverrides {
	return store.currentOverrides(ctx)
}

// resolve merges the overrides on top of the .env baseline.
func (store *runtimeSettingsStore) resolve(ctx context.Context) resolvedSettings {
	return mergeSettings(store.config, store.currentOverrides(ctx))
}

// apply writes the given $set / $unset documents to the overrides record and
// refreshes the cache so the change is visible immediately on this replica.
func (store *runtimeSettingsStore) apply(ctx context.Context, set bson.M, unset bson.M) error {
	update := bson.M{}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if len(update) == 0 {
		return nil
	}
	if _, err := store.collection.UpdateOne(ctx, bson.M{"_id": runtimeSettingsDocID}, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return err
	}
	store.refresh(ctx)
	return nil
}

// resetAll clears every override, returning the deployment to its .env baseline.
func (store *runtimeSettingsStore) resetAll(ctx context.Context) error {
	if _, err := store.collection.DeleteOne(ctx, bson.M{"_id": runtimeSettingsDocID}); err != nil {
		return err
	}
	store.refresh(ctx)
	return nil
}

// mergeSettings applies overrides on top of the .env config and enforces the
// same ceilings as the boot path so a bad override can never exceed them.
func mergeSettings(c Config, o settingsOverrides) resolvedSettings {
	resolved := resolvedSettings{
		TrySandbox:              boolOr(o.TrySandbox, c.TrySandbox),
		TrySandboxTTL:           durationOr(o.TrySandboxTTL, c.TrySandboxTTL),
		BuilderSignupOpen:       boolOr(o.BuilderSignupOpen, c.BuilderSignupOpen),
		BuilderSignupDailyLimit: intOr(o.BuilderSignupDailyLimit, c.BuilderSignupDailyLimit),
		DemoSeed:                boolOr(o.DemoSeed, c.DemoSeed),
		WebhookDelivery:         boolOr(o.WebhookDelivery, c.WebhookDelivery),
		FreeTierMaxApps:         intOr(o.FreeTierMaxApps, c.FreeTierMaxApps),
		FreeTierMaxUsers:        intOr(o.FreeTierMaxUsers, c.FreeTierMaxUsers),
		FreeTierMaxRecords:      intOr(o.FreeTierMaxRecords, c.FreeTierMaxRecords),
		FreeTierMaxRecordBytes:  intOr(o.FreeTierMaxRecordBytes, c.FreeTierMaxRecordBytes),
		VibeMaxSandboxes:        intOr(o.VibeMaxSandboxes, c.VibeMaxSandboxes),
		VibeDailyLimit:          intOr(o.VibeDailyLimit, c.VibeDailyLimit),
		VibeShareTTL:            durationOr(o.VibeShareTTL, c.VibeShareTTL),
	}
	resolved.TrySandboxTTL = clampSandboxTTL(resolved.TrySandboxTTL, 24*time.Hour)
	resolved.VibeShareTTL = clampSandboxTTL(resolved.VibeShareTTL, 72*time.Hour)
	resolved.TrySandboxTTLString = resolved.TrySandboxTTL.String()
	resolved.VibeShareTTLString = resolved.VibeShareTTL.String()
	return resolved
}

// resolvedSettingsFromConfig is the no-override resolution, used when no settings
// store is wired (nil database in unit tests).
func resolvedSettingsFromConfig(c Config) resolvedSettings {
	return mergeSettings(c, settingsOverrides{})
}

// clampSandboxTTL applies the shared ceiling (vibeShareMaxTTL) and a positive
// default so a zero or oversized value is always corrected.
func clampSandboxTTL(ttl, fallback time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = fallback
	}
	if ttl > vibeShareMaxTTL {
		return vibeShareMaxTTL
	}
	return ttl
}

func boolOr(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}
	return fallback
}

func intOr(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

func durationOr(value *string, fallback time.Duration) time.Duration {
	if value != nil {
		if parsed, err := time.ParseDuration(*value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// effectiveSettings resolves the deployment's live settings. It is nil-safe: when
// no settings store is wired (unit tests with a nil database) it falls back to
// the immutable .env config.
func (app *application) effectiveSettings(ctx context.Context) resolvedSettings {
	if app.settings == nil {
		return resolvedSettingsFromConfig(app.config)
	}
	return app.settings.resolve(ctx)
}

// demoSeedEnabled reports whether the storytelling demo should be seeded right
// now, honoring any live operator override.
func (app *application) demoSeedEnabled(ctx context.Context) bool {
	return app.effectiveSettings(ctx).DemoSeed
}

// webhookController wraps the outbox delivery worker so the operator console can
// start and stop background delivery at runtime without a restart.
type webhookController struct {
	app     *application
	mu      sync.Mutex
	worker  *webhookWorker
	enabled bool
}

func newWebhookController(app *application) *webhookController {
	return &webhookController{app: app}
}

// SetEnabled starts or stops the delivery worker to match the desired state. It
// is idempotent, so applying the same state twice is a no-op.
func (controller *webhookController) SetEnabled(enabled bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if enabled == controller.enabled {
		return
	}
	controller.enabled = enabled
	if enabled {
		controller.worker = newWebhookWorker(controller.app)
		controller.worker.Start()
		return
	}
	if controller.worker != nil {
		worker := controller.worker
		controller.worker = nil
		// Stop in the background so toggling off never blocks an HTTP handler on
		// an in-flight delivery.
		go worker.Stop(context.Background())
	}
}

// Enabled reports whether delivery is currently running.
func (controller *webhookController) Enabled() bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.enabled
}

// TriggerDrain kicks an immediate drain pass when delivery is enabled, so an
// operator's "retry now" lands without waiting for the next poll tick. Claims
// are atomic, so a manual drain is safe alongside the worker's own loop.
func (controller *webhookController) TriggerDrain() {
	controller.mu.Lock()
	worker := controller.worker
	controller.mu.Unlock()
	if worker != nil {
		go worker.drain(context.Background())
	}
}

// Stop halts delivery for shutdown.
func (controller *webhookController) Stop(ctx context.Context) {
	controller.mu.Lock()
	worker := controller.worker
	controller.worker = nil
	controller.enabled = false
	controller.mu.Unlock()
	if worker != nil {
		worker.Stop(ctx)
	}
}

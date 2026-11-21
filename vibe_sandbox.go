package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// vibeSandboxDefaultMax caps how many ephemeral tenants can exist at once when
// VIBE_MAX_SANDBOXES is unset.
const vibeSandboxDefaultMax = 1000

const vibeSandboxSweepInterval = 10 * time.Minute

// Sandbox tenants carry tight, fixed quotas so a public share link can never be
// turned into unbounded storage on the shared deployment.
const (
	vibeSandboxMaxUsers       = 50
	vibeSandboxMaxResources   = 200
	vibeSandboxMaxRecordBytes = 16 * 1024
)

// errVibeSandboxFull signals the global sandbox cap is reached, so the lab still
// returns a spec and snippets but without a live demo.
var errVibeSandboxFull = errors.New("vibe sandbox capacity reached")

// vibeOwnerLogin is the one-time admin credential for a sandbox. It is returned
// only in the create response and never stored in or served from the share.
type vibeOwnerLogin struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type vibeSandbox struct {
	record AppRecord
	owner  vibeOwnerLogin
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// provisionVibeSandbox creates a short-lived tenant whose admin is the visitor
// (the "owner") and seeds its owner-authored sections with the spec's samples.
func (app *application) provisionVibeSandbox(ctx context.Context, spec vibeSpec) (*vibeSandbox, error) {
	maximum := app.config.VibeMaxSandboxes
	if maximum <= 0 {
		maximum = vibeSandboxDefaultMax
	}
	count, err := app.countActiveSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	if count >= int64(maximum) {
		return nil, errVibeSandboxFull
	}

	slugSuffix, err := randomHex(6)
	if err != nil {
		return nil, err
	}
	ownerSuffix, err := randomHex(4)
	if err != nil {
		return nil, err
	}
	ownerPassword, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	ownerEmail := "owner-" + ownerSuffix + "@sandbox.invalid"
	expiresAt := time.Now().UTC().Add(app.vibeShareTTL())

	record, _, _, err := app.provisionApp(ctx, provisionAppInput{
		Slug:          "sandbox-" + slugSuffix,
		DisplayName:   spec.Name,
		AdminEmail:    ownerEmail,
		AdminPassword: ownerPassword,
		// The starter talks to the API with bearer tokens and credentials "omit", so
		// a wildcard origin never exposes a cookie session.
		AllowedOrigins: []string{"*"},
		Sandbox:        true,
		ExpiresAt:      &expiresAt,
	})
	if err != nil {
		return nil, err
	}

	if err := app.seedVibeSandbox(ctx, record.ID, spec); err != nil {
		app.logger.Error("vibe sandbox seed failed", "error", err, "app", record.ID.Hex())
	}

	return &vibeSandbox{
		record: record,
		owner:  vibeOwnerLogin{Email: ownerEmail, Password: ownerPassword},
	}, nil
}

// seedVibeSandbox inserts the spec's owner-section samples as public shared records
// so the storefront looks alive before the owner touches anything.
func (app *application) seedVibeSandbox(ctx context.Context, appID bson.ObjectID, spec vibeSpec) error {
	var shared Namespace
	if err := app.database.Collection(namespaceCollectionName).
		FindOne(ctx, bson.M{"appId": appID, "kind": sharedNamespaceKind}).Decode(&shared); err != nil {
		return err
	}
	now := time.Now().UTC()
	documents := []any{}
	for _, section := range spec.ownerSections() {
		for index, sample := range section.Samples {
			slug, err := resourceSlugFromTitle(sampleTitle(section, sample))
			if err != nil {
				slug = section.Type + "-" + itoa(index+1)
			}
			documents = append(documents, Resource{
				ID:          bson.NewObjectID(),
				AppID:       appID,
				NamespaceID: shared.ID,
				CreatorID:   shared.ID, // deployment-owned seed content, no end user
				Type:        section.Type,
				Slug:        slug,
				Title:       sampleTitle(section, sample),
				Summary:     sampleSummary(section, sample),
				Data:        bson.M(sample),
				Visibility:  publicVisibility,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}
	}
	if len(documents) == 0 {
		return nil
	}
	_, err := app.database.Collection(resourceCollectionName).InsertMany(ctx, documents)
	return err
}

// resourceSlugFromTitle derives a unique, route-valid slug from a title.
func resourceSlugFromTitle(title string) (string, error) {
	var builder strings.Builder
	lastHyphen := true
	for _, character := range strings.ToLower(strings.TrimSpace(title)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
			lastHyphen = false
		} else if !lastHyphen {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	base := strings.Trim(builder.String(), "-")
	if len(base) > 48 {
		base = strings.Trim(base[:48], "-")
	}
	if base == "" {
		base = "item"
	}
	suffix, err := randomHex(2)
	if err != nil {
		return "", err
	}
	return base + "-" + suffix, nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// sandboxSweeper purges expired sandbox tenants and all of their data on an
// interval, keeping the store from accumulating dead apps.
type sandboxSweeper struct {
	app      *application
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

func newSandboxSweeper(app *application) *sandboxSweeper {
	return &sandboxSweeper{
		app:      app,
		interval: vibeSandboxSweepInterval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (sweeper *sandboxSweeper) Start() {
	go sweeper.run()
}

func (sweeper *sandboxSweeper) Stop(ctx context.Context) {
	close(sweeper.stop)
	select {
	case <-sweeper.done:
	case <-ctx.Done():
	}
}

func (sweeper *sandboxSweeper) run() {
	defer close(sweeper.done)
	ticker := time.NewTicker(sweeper.interval)
	defer ticker.Stop()
	sweeper.sweep(context.Background())
	for {
		select {
		case <-sweeper.stop:
			return
		case <-ticker.C:
			sweeper.sweep(context.Background())
		}
	}
}

func (sweeper *sandboxSweeper) sweep(ctx context.Context) {
	cursor, err := sweeper.app.database.Collection(appsCollectionName).Find(ctx, bson.M{
		"sandbox":   true,
		"expiresAt": bson.M{"$lte": time.Now().UTC()},
	})
	if err != nil {
		sweeper.app.logger.Error("sandbox sweep query failed", "error", err)
		return
	}
	var expired []AppRecord
	if err := cursor.All(ctx, &expired); err != nil {
		sweeper.app.logger.Error("sandbox sweep decode failed", "error", err)
		return
	}
	for _, record := range expired {
		select {
		case <-sweeper.stop:
			return
		default:
		}
		if err := sweeper.app.purgeSandboxApp(ctx, record); err != nil {
			sweeper.app.logger.Error("sandbox purge failed", "error", err, "app", record.ID.Hex())
		}
	}
}

// purgeSandboxApp deactivates a sandbox tenant and deletes every document it owns
// across all collections, then removes the app record itself.
func (app *application) purgeSandboxApp(ctx context.Context, record AppRecord) error {
	// Deactivate first and evict the cache so nothing can keep using it mid-purge.
	if _, err := app.database.Collection(appsCollectionName).UpdateByID(ctx, record.ID, bson.M{"$set": bson.M{"active": false}}); err != nil {
		return err
	}
	app.appCache.delete(record.PublishableKey)

	// Sessions key on userId, so collect this tenant's users first.
	userIDs, err := app.sandboxUserIDs(ctx, record.ID)
	if err != nil {
		return err
	}
	if len(userIDs) > 0 {
		if _, err := app.database.Collection(sessionCollectionName).DeleteMany(ctx, bson.M{"userId": bson.M{"$in": userIDs}}); err != nil {
			return err
		}
	}

	byAppID := bson.M{"appId": record.ID}
	for _, collection := range []string{resourceCollectionName, namespaceCollectionName, namespaceMembershipCollection, webhookCollectionName, "_users"} {
		if _, err := app.database.Collection(collection).DeleteMany(ctx, byAppID); err != nil {
			return err
		}
	}
	// The outbox stores appId as a hex string.
	if _, err := app.database.Collection(outboxCollectionName).DeleteMany(ctx, bson.M{"appId": record.ID.Hex()}); err != nil {
		return err
	}
	if _, err := app.database.Collection(appsCollectionName).DeleteOne(ctx, bson.M{"_id": record.ID}); err != nil {
		return err
	}
	app.logger.Info("sandbox purged", "app", record.ID.Hex(), "slug", record.Slug)
	app.metrics.recordSandboxPurge()
	return nil
}

func (app *application) sandboxUserIDs(ctx context.Context, appID bson.ObjectID) ([]bson.ObjectID, error) {
	cursor, err := app.database.Collection("_users").Find(ctx, bson.M{"appId": appID})
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	ids := make([]bson.ObjectID, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	return ids, nil
}

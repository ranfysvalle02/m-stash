package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	record     AppRecord
	owner      vibeOwnerLogin
	claimToken string
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// hashClaimToken stores a sandbox's single-use claim token as a SHA-256 digest.
// A plain hash is sufficient because the token is high-entropy (not a password),
// and it keeps the plaintext out of the database.
func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// verifyClaimToken constant-time compares a presented token against a stored hash.
func verifyClaimToken(token, hash string) bool {
	if token == "" || hash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashClaimToken(token)), []byte(hash)) == 1
}

// sandboxOptions configures a short-lived tenant.
type sandboxOptions struct {
	DisplayName string
	// TTL overrides the sandbox lifetime; zero uses the vibe share TTL.
	TTL time.Duration
	// WithAdmin provisions a one-time owner admin login for the sandbox.
	WithAdmin bool
	// Spec, when set, seeds the owner-section samples so the tenant looks alive.
	Spec *vibeSpec
}

// sandboxProvision is the result of creating a sandbox tenant.
type sandboxProvision struct {
	record     AppRecord
	owner      *vibeOwnerLogin
	claimToken string
}

// provisionSandbox creates a short-lived tenant with sandbox quotas and a
// single-use claim token, respecting the global sandbox cap. It is shared by the
// vibe lab (with an admin and seeded samples) and the blank try-it endpoint.
func (app *application) provisionSandbox(ctx context.Context, opts sandboxOptions) (*sandboxProvision, error) {
	settings := app.effectiveSettings(ctx)
	maximum := settings.VibeMaxSandboxes
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
	claimToken, err := randomHex(24)
	if err != nil {
		return nil, err
	}

	ttl := opts.TTL
	if ttl <= 0 {
		ttl = settings.VibeShareTTL
	}
	expiresAt := time.Now().UTC().Add(ttl)

	input := provisionAppInput{
		Slug:        "sandbox-" + slugSuffix,
		DisplayName: opts.DisplayName,
		// The starter talks to the API with bearer tokens and credentials "omit", so
		// a wildcard origin never exposes a cookie session.
		AllowedOrigins: []string{"*"},
		Sandbox:        true,
		ExpiresAt:      &expiresAt,
		ClaimTokenHash: hashClaimToken(claimToken),
	}

	var owner *vibeOwnerLogin
	if opts.WithAdmin {
		ownerSuffix, err := randomHex(4)
		if err != nil {
			return nil, err
		}
		ownerPassword, err := randomHex(12)
		if err != nil {
			return nil, err
		}
		input.AdminEmail = "owner-" + ownerSuffix + "@sandbox.invalid"
		input.AdminPassword = ownerPassword
		owner = &vibeOwnerLogin{Email: input.AdminEmail, Password: ownerPassword}
	}

	record, _, _, err := app.provisionApp(ctx, input)
	if err != nil {
		return nil, err
	}

	if opts.Spec != nil {
		if err := app.seedVibeSandbox(ctx, record.ID, *opts.Spec); err != nil {
			app.logger.Error("vibe sandbox seed failed", "error", err, "app", record.ID.Hex())
		}
	}

	return &sandboxProvision{record: record, owner: owner, claimToken: claimToken}, nil
}

// provisionVibeSandbox creates a short-lived tenant whose admin is the visitor
// (the "owner") and seeds its owner-authored sections with the spec's samples.
func (app *application) provisionVibeSandbox(ctx context.Context, spec vibeSpec) (*vibeSandbox, error) {
	provision, err := app.provisionSandbox(ctx, sandboxOptions{
		DisplayName: spec.Name,
		WithAdmin:   true,
		Spec:        &spec,
	})
	if err != nil {
		return nil, err
	}
	return &vibeSandbox{
		record:     provision.record,
		owner:      *provision.owner,
		claimToken: provision.claimToken,
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
	// Reclaim any cached keys that have expired, so an idle deployment's cache
	// never retains stale sandbox entries between lookups.
	sweeper.app.appCache.sweepExpired(time.Now().UTC())
}

// purgeSandboxApp deactivates a sandbox tenant and deletes every document it owns
// across all collections, then removes the app record itself.
func (app *application) purgeSandboxApp(ctx context.Context, record AppRecord) error {
	// Deactivate first and evict the cache so nothing can keep using it mid-purge.
	// The sandbox:true guard is defence in depth: if the app was claimed in the
	// race window between selection and purge, the update (and later delete) skips
	// it, so a now-live app is never deactivated or destroyed.
	if _, err := app.database.Collection(appsCollectionName).UpdateOne(ctx, bson.M{"_id": record.ID, "sandbox": true}, bson.M{"$set": bson.M{"active": false}}); err != nil {
		return err
	}
	app.appCache.delete(record.PublishableKey)

	if err := app.purgeAppTenantData(ctx, record.ID); err != nil {
		return err
	}

	if _, err := app.database.Collection(appsCollectionName).DeleteOne(ctx, bson.M{"_id": record.ID, "sandbox": true}); err != nil {
		return err
	}
	app.logger.Info("sandbox purged", "app", record.ID.Hex(), "slug", record.Slug)
	app.metrics.recordSandboxPurge()
	return nil
}

// purgeAppTenantData deletes every document an app owns across all tenant
// collections: sessions (keyed by userId), resources, namespaces, namespace
// memberships, webhooks, users, and outbox entries (which store appId as a hex
// string). It leaves the app record itself untouched, so it is shared by the
// sandbox sweeper and owner-initiated app deletion, which each apply their own
// guard (sandbox:true vs ownerId) when removing the app document.
func (app *application) purgeAppTenantData(ctx context.Context, appID bson.ObjectID) error {
	// Sessions key on userId, so collect this tenant's users first.
	userIDs, err := app.appUserIDs(ctx, appID)
	if err != nil {
		return err
	}
	if len(userIDs) > 0 {
		if _, err := app.database.Collection(sessionCollectionName).DeleteMany(ctx, bson.M{"userId": bson.M{"$in": userIDs}}); err != nil {
			return err
		}
	}

	byAppID := bson.M{"appId": appID}
	for _, collection := range []string{resourceCollectionName, namespaceCollectionName, namespaceMembershipCollection, webhookCollectionName, "_users"} {
		if _, err := app.database.Collection(collection).DeleteMany(ctx, byAppID); err != nil {
			return err
		}
	}
	// The outbox stores appId as a hex string.
	if _, err := app.database.Collection(outboxCollectionName).DeleteMany(ctx, bson.M{"appId": appID.Hex()}); err != nil {
		return err
	}
	return nil
}

func (app *application) appUserIDs(ctx context.Context, appID bson.ObjectID) ([]bson.ObjectID, error) {
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

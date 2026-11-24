package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Public stats: aggregate, build-in-the-open numbers for the landing page. They
// are counts only — never names, emails, apps, or records — and are computed
// at most once a minute however often they are requested. Operators can turn
// them off with PUBLIC_STATS=off or live from /admin.

const publicStatsTTL = time.Minute

var errPublicStatsWarming = errors.New("public stats are still loading")

type publicStats struct {
	// Stashers is every registered person: builders plus the users of live
	// (non-sandbox) apps. Sandbox users are excluded because they expire.
	Stashers   int64     `json:"stashers"`
	Builders   int64     `json:"builders"`
	AppUsers   int64     `json:"appUsers"`
	LiveApps   int64     `json:"liveApps"`
	Records    int64     `json:"records"`
	Vibes30d   int64     `json:"vibes30d"`
	UpdatedAt  time.Time `json:"updatedAt"`
	WindowDays int       `json:"vibesWindowDays"`
}

type publicStatsCache struct {
	mu         sync.Mutex
	stats      publicStats
	loaded     bool
	refreshing bool
	// attemptedAt stamps every refresh, successful or not, so a failing database
	// is retried at most once per TTL rather than on every request.
	attemptedAt time.Time
}

func (app *application) handlePublicStats(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if !app.effectiveSettings(r.Context()).PublicStats {
		writeError(w, http.StatusNotFound, "Public stats are turned off on this deployment")
		return
	}
	stats, err := app.cachedPublicStats(r.Context())
	if err != nil {
		app.logger.Error("public stats failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeError(w, http.StatusServiceUnavailable, "Stats are unavailable right now")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{"data": stats})
}

// cachedPublicStats serves the last good numbers and refreshes them at most
// once per TTL, outside the lock: concurrent callers never queue behind a slow
// query, and a failing database is not hammered.
func (app *application) cachedPublicStats(ctx context.Context) (publicStats, error) {
	return app.publicStats.get(ctx, app.computePublicStats)
}

func (cache *publicStatsCache) get(ctx context.Context, compute func(context.Context) (publicStats, error)) (publicStats, error) {
	cache.mu.Lock()
	stale := time.Since(cache.attemptedAt) >= publicStatsTTL
	if !stale || cache.refreshing {
		stats, loaded := cache.stats, cache.loaded
		cache.mu.Unlock()
		if !loaded {
			return publicStats{}, errPublicStatsWarming
		}
		return stats, nil
	}
	cache.refreshing = true
	cache.attemptedAt = time.Now()
	cache.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	stats, err := compute(queryCtx)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.refreshing = false
	if err == nil {
		cache.stats, cache.loaded = stats, true
		return stats, nil
	}
	if cache.loaded {
		return cache.stats, nil
	}
	return publicStats{}, err
}

func (app *application) computePublicStats(ctx context.Context) (publicStats, error) {
	stats := publicStats{UpdatedAt: time.Now().UTC(), WindowDays: vibeFunnelWindowDays}

	cursor, err := app.database.Collection(appsCollectionName).Find(ctx,
		bson.M{"sandbox": bson.M{"$ne": true}, "active": true},
		options.Find().SetProjection(bson.M{"_id": 1}),
	)
	if err != nil {
		return stats, err
	}
	var apps []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &apps); err != nil {
		return stats, err
	}
	liveIDs := make(bson.A, 0, len(apps))
	for _, record := range apps {
		liveIDs = append(liveIDs, record.ID)
	}
	stats.LiveApps = int64(len(liveIDs))

	users := app.database.Collection("_users")
	if stats.Builders, err = users.CountDocuments(ctx, bson.M{"appId": bson.NilObjectID, "role": builderRole}); err != nil {
		return stats, err
	}
	if stats.AppUsers, err = users.CountDocuments(ctx, bson.M{"appId": bson.M{"$in": liveIDs}}); err != nil {
		return stats, err
	}
	stats.Stashers = stats.Builders + stats.AppUsers
	// Records have no appId index, so a filtered count would scan the whole
	// collection; the collection estimate is O(1) and counts every stashed record.
	if stats.Records, err = app.database.Collection(resourceCollectionName).EstimatedDocumentCount(ctx); err != nil {
		return stats, err
	}
	since := time.Now().UTC().Add(-time.Duration(vibeFunnelWindowDays) * 24 * time.Hour)
	if stats.Vibes30d, err = app.database.Collection(vibeRunsCollection).CountDocuments(ctx, bson.M{"createdAt": bson.M{"$gte": since}}); err != nil {
		return stats, err
	}
	return stats, nil
}

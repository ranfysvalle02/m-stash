package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const authRateLimitCollection = "_m_stash_auth_rate_limits"

type authRateLimiter struct {
	collection *mongo.Collection
	limit      int
	window     time.Duration
}

func newAuthRateLimiter(database *mongo.Database, limit int, window time.Duration) *authRateLimiter {
	return &authRateLimiter{
		collection: database.Collection(authRateLimitCollection),
		limit:      limit,
		window:     window,
	}
}

func ensureAuthRateLimitIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(authRateLimitCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return fmt.Errorf("create authentication rate-limit TTL index: %w", err)
	}
	return nil
}

// allow consumes one attempt in a fixed window shared by every service replica.
func (limiter *authRateLimiter) allow(ctx context.Context, scope, key string) (bool, error) {
	now := time.Now().UTC()
	windowStart := now.Truncate(limiter.window)
	identifier := rateLimitIdentifier(scope, key, windowStart)
	filter := bson.M{"_id": identifier, "attempts": bson.M{"$lt": limiter.limit}}
	update := bson.M{
		"$inc": bson.M{"attempts": 1},
		"$setOnInsert": bson.M{
			"scope":       scope,
			"windowStart": windowStart,
			"expiresAt":   windowStart.Add(2 * limiter.window),
		},
	}
	findOptions := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	for attempt := 0; attempt < 3; attempt++ {
		var record struct {
			Attempts int `bson:"attempts"`
		}
		err := limiter.collection.FindOneAndUpdate(ctx, filter, update, findOptions).Decode(&record)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		if mongo.IsDuplicateKeyError(err) {
			var existing struct {
				Attempts int `bson:"attempts"`
			}
			lookupErr := limiter.collection.FindOne(ctx, bson.M{"_id": identifier}).Decode(&existing)
			if lookupErr == nil && existing.Attempts >= limiter.limit {
				return false, nil
			}
			if lookupErr != nil && !errors.Is(lookupErr, mongo.ErrNoDocuments) {
				return false, lookupErr
			}
			continue
		}
		return false, err
	}
	return false, errors.New("authentication rate-limit contention exceeded retry budget")
}

func rateLimitIdentifier(scope, key string, windowStart time.Time) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + key + "\x00" + windowStart.Format(time.RFC3339Nano)))
	return hex.EncodeToString(digest[:])
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy && r.Header.Get("X-Forwarded-For") != "" {
		fwd := r.Header.Get("X-Forwarded-For")
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (app *application) rateLimitMiddleware(limiter *authRateLimiter, scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed, err := limiter.allow(r.Context(), scope, clientIP(r, app.config.TrustProxy))
		if err != nil {
			app.logger.Error("authentication rate-limit check failed", "request_id", requestIDFromContext(r.Context()), "scope", scope, "error", err)
			writeError(w, http.StatusServiceUnavailable, "Authentication is temporarily unavailable")
			return
		}
		if !allowed {
			app.metrics.recordRateLimitRejection()
			writeError(w, http.StatusTooManyRequests, "Too many attempts, please try again later")
			return
		}
		next(w, r)
	}
}

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type application struct {
	config      Config
	database    *mongo.Database
	mongoClient *mongo.Client
	metrics     *metricsRegistry
	logger      *slog.Logger
}

func newApplication(appConfig Config, database *mongo.Database, client *mongo.Client) *application {
	return &application{
		config:      appConfig,
		database:    database,
		mongoClient: client,
		metrics:     newMetricsRegistry(),
		logger:      slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

func (app *application) Handler(rateLimiter *authRateLimiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/signup", app.corsMiddleware(app.rateLimitMiddleware(rateLimiter, "signup", app.handleSignUp)))
	mux.HandleFunc("/v1/auth/login", app.corsMiddleware(app.rateLimitMiddleware(rateLimiter, "login", app.handleLogin)))
	mux.HandleFunc("/v1/auth/refresh", app.corsMiddleware(app.handleSessionRefresh))
	mux.HandleFunc("/v1/auth/logout", app.corsMiddleware(app.handleSessionLogout))
	mux.HandleFunc("/v1/auth/verify", app.corsMiddleware(app.authenticate(handleTokenVerification)))
	mux.HandleFunc("/v1/me/profile", app.corsMiddleware(app.authenticate(app.handleMeProfile)))
	mux.HandleFunc("/v1/me/stashes", app.corsMiddleware(app.authenticate(app.handleMeStashes)))
	mux.HandleFunc("/v1/me/stashes/", app.corsMiddleware(app.authenticate(app.handleMeStashes)))
	mux.HandleFunc("/.well-known/m-stash.json", app.corsMiddleware(app.handleServiceManifest))
	mux.HandleFunc("/v1/public/stashes", app.corsMiddleware(app.handlePublicDiscovery))
	mux.HandleFunc("/v1/public/stashes/", app.corsMiddleware(app.handlePublicDiscovery))
	mux.HandleFunc("/v1/public/profiles/", app.corsMiddleware(app.handlePublicProfile))
	mux.HandleFunc("/v1/db/", app.corsMiddleware(app.authenticate(app.handleDatabaseProxy)))
	mux.HandleFunc("/v1/ws/", app.authenticate(app.handleDatabaseWebSocket))
	mux.HandleFunc("/metrics", app.handleMetrics)
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/readyz", app.handleReadiness)
	mux.HandleFunc("/", app.handleFrontend)
	return app.requestIDMiddleware(app.securityHeadersMiddleware(mux))
}

func (app *application) handleServiceManifest(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": "v1",
		"service":    "m-stash",
		"authentication": map[string]any{
			"scheme":               "Bearer",
			"verificationEndpoint": "/v1/auth/verify",
			"issuer":               app.config.JWTIssuer,
			"audience":             app.config.JWTAudience,
		},
		"capabilities": map[string]any{
			"publicDiscovery": "/v1/public/stashes",
			"webSocket":       "/v1/ws/{collection}",
			"databaseProxy":   "/v1/db/{collection}/{action}",
			"eventOutbox": map[string]any{
				"delivery":      "transactional-outbox",
				"schemaVersion": outboxSchemaVersion,
			},
			"metrics": map[string]any{
				"enabled":  app.config.MetricsToken != "",
				"endpoint": "/metrics",
			},
		},
	})
}

func (app *application) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time": time.Now().String()})
}

func (app *application) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := app.mongoClient.Ping(ctx, nil); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

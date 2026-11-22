package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type application struct {
	config                    Config
	database                  *mongo.Database
	mongoClient               *mongo.Client
	metrics                   *metricsRegistry
	logger                    *slog.Logger
	appCache                  *appKeyCache
	uiAppKey                  string
	vibeLimiter               *authRateLimiter
	vibeDailyLimiter          *authRateLimiter
	builderSignupLimiter      *authRateLimiter
	builderSignupDailyLimiter *authRateLimiter
	tryLimiter                *authRateLimiter
	// settings is the live runtime overlay on top of the immutable .env config.
	settings *runtimeSettingsStore
	// webhooks drains the outbox; it can be toggled on and off at runtime.
	webhooks *webhookController
	// sweeper purges expired sandbox tenants and runs unconditionally so runtime
	// toggles and operator tinker sandboxes are always swept.
	sweeper *sandboxSweeper
	// vibeCooldownUntil (unix nanos) skips the model after a key-level failure so requests don't each pay for a doomed call.
	vibeCooldownUntil atomic.Int64
}

func newApplication(appConfig Config, database *mongo.Database, client *mongo.Client) *application {
	app := &application{
		config:      appConfig,
		database:    database,
		mongoClient: client,
		metrics:     newMetricsRegistry(),
		logger:      slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		appCache:    newAppKeyCache(30 * time.Second),
	}
	// The live settings overlay needs a database; unit tests pass nil and fall
	// back to the immutable .env config via effectiveSettings.
	if database != nil {
		app.settings = newRuntimeSettingsStore(database, appConfig)
	}
	return app
}

func (app *application) Handler(rateLimiter *authRateLimiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/apps", app.corsMiddleware(app.requirePlatformToken(app.handleApps)))
	mux.HandleFunc("/v1/builder/accounts", app.corsMiddleware(app.requirePlatformToken(app.handleBuilderAccounts)))
	mux.HandleFunc("/v1/builder/auth/login", app.corsMiddleware(app.rateLimitMiddleware(rateLimiter, "builder-login", app.handleBuilderLogin)))
	mux.HandleFunc("/v1/builder/auth/signup", app.corsMiddleware(app.rateLimitMiddleware(app.builderSignupLimiter, "builder-signup", app.handleBuilderSignup)))
	mux.HandleFunc("/v1/builder/auth/refresh", app.corsMiddleware(app.handleSessionRefresh))
	mux.HandleFunc("/v1/builder/auth/logout", app.corsMiddleware(app.handleSessionLogout))
	mux.HandleFunc("/v1/builder/me", app.corsMiddleware(app.authenticateBuilder(app.handleBuilderMe)))
	mux.HandleFunc("/v1/builder/apps", app.corsMiddleware(app.authenticateBuilder(app.handleBuilderApps)))
	mux.HandleFunc("/v1/builder/apps/", app.corsMiddleware(app.authenticateBuilder(app.handleBuilderApp)))
	// Operator control plane. Every route requires the super-operator role.
	mux.HandleFunc("/v1/admin/overview", app.corsMiddleware(app.authenticateOperator(app.handleAdminOverview)))
	mux.HandleFunc("/v1/admin/settings", app.corsMiddleware(app.authenticateOperator(app.handleAdminSettings)))
	mux.HandleFunc("/v1/admin/vibe/clear-cooldown", app.corsMiddleware(app.authenticateOperator(app.handleAdminClearVibeCooldown)))
	mux.HandleFunc("/v1/admin/apps", app.corsMiddleware(app.authenticateOperator(app.handleAdminApps)))
	mux.HandleFunc("/v1/admin/apps/", app.corsMiddleware(app.authenticateOperator(app.handleAdminApp)))
	mux.HandleFunc("/v1/admin/sandboxes/sweep", app.corsMiddleware(app.authenticateOperator(app.handleAdminSweep)))
	mux.HandleFunc("/v1/admin/builders", app.corsMiddleware(app.authenticateOperator(app.handleAdminBuilders)))
	mux.HandleFunc("/v1/admin/builders/", app.corsMiddleware(app.authenticateOperator(app.handleAdminBuilder)))
	mux.HandleFunc("/v1/admin/outbox", app.corsMiddleware(app.authenticateOperator(app.handleAdminOutbox)))
	mux.HandleFunc("/v1/admin/outbox/", app.corsMiddleware(app.authenticateOperator(app.handleAdminOutboxItem)))
	mux.HandleFunc("/v1/admin/tinker/sandbox", app.corsMiddleware(app.authenticateOperator(app.handleAdminTinkerSandbox)))
	mux.HandleFunc("/v1/auth/signup", app.resolveApp(app.corsMiddleware(app.rateLimitMiddleware(rateLimiter, "signup", app.handleSignUp))))
	mux.HandleFunc("/v1/auth/login", app.resolveApp(app.corsMiddleware(app.rateLimitMiddleware(rateLimiter, "login", app.handleLogin))))
	mux.HandleFunc("/v1/auth/refresh", app.resolveApp(app.corsMiddleware(app.handleSessionRefresh)))
	mux.HandleFunc("/v1/auth/logout", app.resolveApp(app.corsMiddleware(app.handleSessionLogout)))
	mux.HandleFunc("/v1/auth/verify", app.resolveApp(app.corsMiddleware(app.authenticate(handleTokenVerification))))
	mux.HandleFunc("/v1/me/namespace", app.resolveApp(app.corsMiddleware(app.authenticate(app.handleMeNamespace))))
	mux.HandleFunc("/v1/me/resources/", app.resolveApp(app.corsMiddleware(app.authenticate(app.handleMeResources))))
	mux.HandleFunc("/v1/shared/namespace", app.resolveApp(app.corsMiddleware(app.authenticate(app.handleSharedNamespace))))
	mux.HandleFunc("/v1/shared/resources/", app.resolveApp(app.corsMiddleware(app.authenticateSharedResource(app.handleSharedResources))))
	mux.HandleFunc("/v1/shared/webhooks", app.resolveApp(app.corsMiddleware(app.authenticateSharedResource(app.handleSharedWebhooks))))
	mux.HandleFunc("/v1/shared/webhooks/", app.resolveApp(app.corsMiddleware(app.authenticateSharedResource(app.handleSharedWebhooks))))
	mux.HandleFunc("/.well-known/m-stash.json", app.corsMiddleware(app.handleServiceManifest))
	mux.HandleFunc("/skill.md", app.corsMiddleware(app.handleSkill))
	mux.HandleFunc("/v1/vibe", app.corsMiddleware(app.handleVibe))
	mux.HandleFunc("/v1/vibe/recipes", app.corsMiddleware(app.handleVibeRecipes))
	mux.HandleFunc("/v1/vibe/", app.corsMiddleware(app.handleVibeShare))
	mux.HandleFunc("/v1/try", app.corsMiddleware(app.rateLimitMiddleware(app.tryLimiter, "try-sandbox", app.handleTry)))
	mux.HandleFunc("/v1/public/shared/resources/", app.resolveApp(app.corsMiddleware(app.handlePublicSharedResources)))
	mux.HandleFunc("/v1/public/", app.resolveApp(app.corsMiddleware(app.handlePublicNamespace)))
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

	settings := app.effectiveSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":    "v1",
		"service":       "m-stash",
		"documentation": "/llms.txt",
		"authentication": map[string]any{
			"scheme":               "Bearer",
			"applicationKeyHeader": appKeyHeader,
			"verificationEndpoint": "/v1/auth/verify",
			"issuer":               app.config.JWTIssuer,
			"audience":             app.config.JWTAudience,
		},
		"capabilities": map[string]any{
			"applications": map[string]string{
				"provision": "/v1/apps",
				"keyHeader": appKeyHeader,
			},
			"namespace": map[string]string{
				"mine":      "/v1/me/namespace",
				"resources": "/v1/me/resources/{type}/{slug}",
				"public":    "/v1/public/{username}/{type}/{slug}",
			},
			"sharedNamespace": map[string]string{
				"namespace":       "/v1/shared/namespace",
				"resources":       "/v1/shared/resources/{type}/{slug}",
				"publicResources": "/v1/public/shared/resources/{type}/{slug}",
				"read":            "authenticated users",
				"write":           "administrators or trusted services",
			},
			"eventOutbox": map[string]any{
				"delivery":      "transactional-outbox",
				"schemaVersion": outboxSchemaVersion,
			},
			"metrics": map[string]any{
				"enabled":  app.config.MetricsToken != "",
				"endpoint": "/metrics",
			},
			"vibe": map[string]any{
				"enabled":         app.vibeAvailable(r.Context()),
				"aiEnabled":       app.vibeEnabled(),
				"endpoint":        "/v1/vibe",
				"recipesEndpoint": "/v1/vibe/recipes",
			},
			// A plug-and-play SKILL.md for coding agents (Cursor, Claude Code, ...),
			// stamped with this deployment's origin.
			"agentSkill": map[string]any{
				"endpoint": "/skill.md",
			},
			"trySandbox": map[string]any{
				"enabled":  settings.TrySandbox,
				"endpoint": "/v1/try",
			},
			"builderSignup": map[string]any{
				"enabled":  settings.BuilderSignupOpen,
				"endpoint": "/v1/builder/auth/signup",
			},
			"claim": map[string]any{
				"endpoint": "/v1/builder/apps/claim",
			},
			// operatorBootstrap tells the console whether ADMIN_EMAIL/ADMIN_PASSWORD
			// are configured, so it can point operators at /admin. The email itself
			// is never exposed.
			"operatorBootstrap": map[string]any{
				"configured": app.config.AdminEmail != "",
				"endpoint":   "/v1/admin",
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

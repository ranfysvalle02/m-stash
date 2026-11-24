package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	appConfig, err := loadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	log.Println("🔌 Connecting to MongoDB...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(appConfig.MongoURI))
	if err != nil {
		log.Fatalf("❌ Failed to connect to Mongo: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("❌ Failed to reach Mongo: %v", err)
	}
	database := client.Database(appConfig.Database)
	if err := verifyTransactionSupport(ctx, client); err != nil {
		log.Fatalf("MongoDB configuration error: %v", err)
	}
	_, err = database.Collection("_users").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "appId", Value: 1}, {Key: "email", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Fatalf("Failed to create user email index: %v", err)
	}
	if err := ensureAppIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create application indexes: %v", err)
	}
	if err := ensureNamespaceIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create namespace indexes: %v", err)
	}
	if err := ensureOutboxIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create outbox indexes: %v", err)
	}
	if err := ensureAuthRateLimitIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create authentication rate-limit index: %v", err)
	}
	if err := ensureSessionIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create browser-session indexes: %v", err)
	}
	if err := ensureWebhookIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create webhook indexes: %v", err)
	}
	if err := ensureVibeShareIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create vibe share index: %v", err)
	}
	if err := ensureVibeRunIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create vibe run indexes: %v", err)
	}
	if err := ensureOAuthIndexes(ctx, database); err != nil {
		log.Fatalf("Failed to create OAuth identity indexes: %v", err)
	}
	app := newApplication(appConfig, database, client)
	log.Printf("✅ Connected to Mongo database: '%s'", appConfig.Database)
	if uiAppKey, err := app.ensureDefaultApp(ctx); err != nil {
		log.Printf("⚠️  Could not ensure default application (UI may need UI_APP_KEY): %v", err)
	} else {
		app.uiAppKey = uiAppKey
		log.Printf("🪪 UI bound to application key %s", uiAppKey)
		if record, err := app.lookupApp(ctx, uiAppKey); err != nil {
			log.Printf("⚠️  Could not resolve default application: %v", err)
		} else {
			if err := app.ensureBootstrapAdmin(ctx, record.ID); err != nil {
				log.Printf("⚠️  Could not ensure bootstrap operator: %v", err)
			} else if appConfig.AdminEmail != "" {
				log.Printf("🔐 Bootstrap operator ready (%s) — sign in at /admin", appConfig.AdminEmail)
			}
			if err := app.syncDemoData(ctx, record.ID); err != nil {
				log.Printf("⚠️  Demo data sync failed: %v", err)
			} else if app.demoSeedEnabled(ctx) {
				log.Print("🪄 Demo data seeded (toggle it live from /admin or set DEMO_DATA=off)")
			} else {
				log.Print("🧹 Demo data disabled (DEMO_DATA=off)")
			}
		}
	}
	if appConfig.MetricsToken == "" {
		log.Print("Metrics endpoint disabled because METRICS_TOKEN is not configured")
	}
	if len(appConfig.AllowedOrigins) == 1 && appConfig.AllowedOrigins[0] == "*" {
		log.Print("⚠️  ALLOWED_ORIGINS is unset — CORS permits all origins. Set explicit origins before production browser use.")
	}

	authRateLimiter := newAuthRateLimiter(database, 10, 5*time.Minute)
	app.vibeLimiter = newAuthRateLimiter(database, 8, time.Minute)
	app.vibeDailyLimiter = newAuthRateLimiter(database, appConfig.VibeDailyLimit, 24*time.Hour)
	app.builderSignupLimiter = newAuthRateLimiter(database, 5, time.Hour)
	app.builderSignupDailyLimiter = newAuthRateLimiter(database, appConfig.BuilderSignupDailyLimit, 24*time.Hour)
	app.tryLimiter = newAuthRateLimiter(database, 5, 10*time.Minute)
	app.oauthLimiter = newAuthRateLimiter(database, 20, 5*time.Minute)
	if app.vibeEnabled() {
		log.Printf("✨ Vibe lab enabled (model %s, daily cap %d)", appConfig.GeminiModel, appConfig.VibeDailyLimit)
	} else {
		log.Print("Vibe lab disabled because GEMINI_API_KEY is not configured")
	}
	if appConfig.BuilderSignupOpen {
		log.Printf("🪄 Self-serve builder signup enabled (free plan: %d apps, daily cap %d)", appConfig.FreeTierMaxApps, appConfig.BuilderSignupDailyLimit)
	}
	if appConfig.TrySandbox {
		log.Printf("🧪 Try-it sandbox enabled (TTL %s)", appConfig.TrySandboxTTL)
	}
	// The sweeper purges expired sandboxes, which the vibe lab, the try-it
	// endpoint, and operator tinker sandboxes all create. It runs unconditionally
	// so flipping TRY_SANDBOX on at runtime never leaves tenants un-swept.
	app.sweeper = newSandboxSweeper(app)
	app.sweeper.Start()
	log.Printf("🧹 Sandbox sweeper started (every %s)", vibeSandboxSweepInterval)

	// The webhook controller drains the outbox and can be toggled on and off at
	// runtime from the operator console; it starts enabled when the effective
	// WEBHOOK_DELIVERY setting is on at boot.
	app.webhooks = newWebhookController(app)
	if app.effectiveSettings(ctx).WebhookDelivery {
		app.webhooks.SetEnabled(true)
		log.Printf("📡 Webhook delivery worker started (poll %s, max attempts %d)", appConfig.WebhookPollInterval, appConfig.WebhookMaxAttempts)
	}
	server := &http.Server{
		Addr:              ":" + appConfig.Port,
		Handler:           app.Handler(authRateLimiter),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("🚀 Mongo Auth Gateway listening on http://localhost:%s", appConfig.Port)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if app.webhooks != nil {
			app.webhooks.Stop(ctx)
		}
		if app.sweeper != nil {
			app.sweeper.Stop(ctx)
		}
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown failed: %v", err)
		}
		if err := client.Disconnect(ctx); err != nil {
			log.Printf("MongoDB disconnect failed: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("❌ Server crashed: %v", err)
	}
}

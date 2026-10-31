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
	app := newApplication(appConfig, database, client)
	log.Printf("✅ Connected to Mongo database: '%s'", appConfig.Database)
	if appConfig.MetricsToken == "" {
		log.Print("Metrics endpoint disabled because METRICS_TOKEN is not configured")
	}

	authRateLimiter := newAuthRateLimiter(database, 10, 5*time.Minute)
	var deliveryWorker *webhookWorker
	if appConfig.WebhookDelivery {
		deliveryWorker = newWebhookWorker(app)
		deliveryWorker.Start()
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
		if deliveryWorker != nil {
			deliveryWorker.Stop(ctx)
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

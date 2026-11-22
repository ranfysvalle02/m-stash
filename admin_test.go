package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestValidateBootstrapAdminCredentials(t *testing.T) {
	if email, err := validateBootstrapAdminCredentials("", ""); err != nil || email != "" {
		t.Fatalf("empty admin credentials = %q, %v", email, err)
	}
	if _, err := validateBootstrapAdminCredentials("admin@example.com", ""); err == nil {
		t.Fatal("partial admin credentials were accepted")
	}
	email, err := validateBootstrapAdminCredentials(" Admin@Example.com ", "this-is-a-long-admin-password")
	if err != nil {
		t.Fatalf("valid admin credentials rejected: %v", err)
	}
	if email != "admin@example.com" {
		t.Fatalf("admin email = %q, want normalized email", email)
	}
}

func TestPrimitiveRegexEscapesMetacharacters(t *testing.T) {
	pattern := primitiveRegex(".*danger")
	if got := pattern["$regex"]; got != regexp.QuoteMeta(".*danger") {
		t.Fatalf("$regex = %v, want escaped literal", got)
	}
	if pattern["$options"] != "i" {
		t.Fatalf("$options = %v, want case-insensitive", pattern["$options"])
	}
}

func TestOutboxRetryUpdateResetsToPending(t *testing.T) {
	set, ok := outboxRetryUpdate()["$set"].(bson.M)
	if !ok {
		t.Fatal("retry update must carry a $set document")
	}
	if set["delivery.state"] != "pending" {
		t.Fatalf("delivery.state = %v, want pending", set["delivery.state"])
	}
	if set["delivery.attempts"] != 0 {
		t.Fatalf("delivery.attempts = %v, want 0", set["delivery.attempts"])
	}
	if _, present := set["delivery.availableAt"]; !present {
		t.Fatal("retry update must set delivery.availableAt for immediate redelivery")
	}
	unset, ok := outboxRetryUpdate()["$unset"].(bson.M)
	if !ok {
		t.Fatal("retry update must carry a $unset document")
	}
	if _, present := unset["delivery.expiresAt"]; !present {
		t.Fatal("retry update must clear delivery.expiresAt so a retried event is not TTL-reaped")
	}
}

func TestAllowWithLimitUnlimited(t *testing.T) {
	limiter := &authRateLimiter{}
	for _, limit := range []int{0, -1} {
		allowed, err := limiter.allowWithLimit(context.Background(), "scope", "key", limit)
		if err != nil {
			t.Fatalf("allowWithLimit(%d) error: %v", limit, err)
		}
		if !allowed {
			t.Fatalf("allowWithLimit(%d) = false, want unlimited (true)", limit)
		}
	}
}

// TestEnvironmentPostureHidesSecrets is a security guard: the operator settings
// view must report only whether each sensitive value is configured, never the
// value itself — not the metrics or platform tokens, and not the admin email.
func TestEnvironmentPostureHidesSecrets(t *testing.T) {
	const metricsToken = "metrics-secret-abcdefghijklmnopqrstuvwx"
	const platformToken = "platform-secret-abcdefghijklmnopqrstuvwx"
	const adminEmail = "secret-admin@example.com"
	app := &application{config: Config{
		MetricsToken:        metricsToken,
		PlatformToken:       platformToken,
		AdminEmail:          adminEmail,
		GeminiModel:         "gemini-x",
		Database:            "mstash",
		PublicBaseURL:       "https://example.com",
		AllowedOrigins:      []string{"https://example.com"},
		SessionCookieSecure: true,
		TrustProxy:          true,
	}}
	posture := app.environmentPosture()

	if posture["metricsTokenConfigured"] != true {
		t.Fatal("metricsTokenConfigured should be true when a token is set")
	}
	if posture["platformTokenConfigured"] != true {
		t.Fatal("platformTokenConfigured should be true when a token is set")
	}
	if posture["operatorConfigured"] != true {
		t.Fatal("operatorConfigured should be true when ADMIN_EMAIL is set")
	}

	blob, err := json.Marshal(posture)
	if err != nil {
		t.Fatalf("marshal posture: %v", err)
	}
	serialized := string(blob)
	for _, secret := range []string{metricsToken, platformToken, adminEmail} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("environment posture leaked a secret: %q", secret)
		}
	}
}

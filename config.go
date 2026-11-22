package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                string
	MongoURI            string
	Database            string
	JWTSecret           string
	JWTIssuer           string
	JWTAudience         string
	TrustProxy          bool
	MetricsToken        string
	AllowedOrigins      []string
	AccessTokenTTL      time.Duration
	RefreshSessionTTL   time.Duration
	SessionCookieSecure bool
	PlatformToken       string
	UIAppKey            string
	DefaultAppSlug      string
	DefaultAppName      string
	DemoSeed            bool
	WebhookDelivery     bool
	WebhookPollInterval time.Duration
	WebhookMaxAttempts  int
	GeminiAPIKey        string
	GeminiModel         string
	VibeDailyLimit      int
	VibeShareTTL        time.Duration
	VibeMaxSandboxes    int
	PublicBaseURL       string
	// OAuth client credentials for social-login adapters. A provider is enabled
	// only when both its ID and secret are set; a half-configured pair fails boot.
	// Secrets live only in the environment, never in MongoDB.
	GitHubClientID     string
	GitHubClientSecret string
	GoogleClientID     string
	GoogleClientSecret string
	// BuilderSignupOpen enables self-serve builder account creation. When false,
	// builder accounts can only be bootstrapped with the platform token.
	BuilderSignupOpen       bool
	BuilderSignupDailyLimit int
	FreeTierMaxApps         int
	FreeTierMaxUsers        int
	FreeTierMaxRecords      int
	FreeTierMaxRecordBytes  int
	// TrySandbox enables the blank "try it live" sandbox endpoint for the guide.
	TrySandbox    bool
	TrySandboxTTL time.Duration
	// AdminEmail and AdminPassword bootstrap the deployment super-operator on boot.
	// Both empty disables the bootstrap; set together they upsert an operator
	// (control-plane) principal and a hosted-demo admin that share these
	// credentials, so an operator can sign straight into /admin and /login with
	// no platform token or curl. These are the .env baseline and never mutate.
	AdminEmail    string
	AdminPassword string
}

func loadConfig() (Config, error) {
	trustProxy, err := getEnvBool("TRUST_PROXY", false)
	if err != nil {
		return Config{}, err
	}
	accessTokenTTL, err := getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	refreshSessionTTL, err := getEnvDuration("REFRESH_SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	sessionCookieSecure, err := getEnvBool("SESSION_COOKIE_SECURE", true)
	if err != nil {
		return Config{}, err
	}
	demoSeed, err := getEnvBool("DEMO_DATA", true)
	if err != nil {
		return Config{}, err
	}
	webhookDelivery, err := getEnvBool("WEBHOOK_DELIVERY", false)
	if err != nil {
		return Config{}, err
	}
	webhookPollInterval, err := getEnvDuration("WEBHOOK_POLL_INTERVAL", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	webhookMaxAttempts, err := getEnvInt("WEBHOOK_MAX_ATTEMPTS", 6)
	if err != nil {
		return Config{}, err
	}
	vibeDailyLimit, err := getEnvInt("VIBE_DAILY_LIMIT", 500)
	if err != nil {
		return Config{}, err
	}
	vibeShareTTL, err := getEnvDuration("VIBE_SHARE_TTL", 72*time.Hour)
	if err != nil {
		return Config{}, err
	}
	vibeMaxSandboxes, err := getEnvInt("VIBE_MAX_SANDBOXES", 1000)
	if err != nil {
		return Config{}, err
	}
	builderSignupOpen, err := getEnvToggle("BUILDER_SIGNUP", "open", "off", false)
	if err != nil {
		return Config{}, err
	}
	builderSignupDailyLimit, err := getEnvInt("BUILDER_SIGNUP_DAILY_LIMIT", 100)
	if err != nil {
		return Config{}, err
	}
	freeTierMaxApps, err := getEnvInt("FREE_TIER_MAX_APPS", 3)
	if err != nil {
		return Config{}, err
	}
	freeTierMaxUsers, err := getEnvInt("FREE_TIER_MAX_USERS", 1000)
	if err != nil {
		return Config{}, err
	}
	freeTierMaxRecords, err := getEnvInt("FREE_TIER_MAX_RECORDS", 2000)
	if err != nil {
		return Config{}, err
	}
	freeTierMaxRecordBytes, err := getEnvInt("FREE_TIER_MAX_RECORD_BYTES", 64*1024)
	if err != nil {
		return Config{}, err
	}
	trySandbox, err := getEnvToggle("TRY_SANDBOX", "on", "off", false)
	if err != nil {
		return Config{}, err
	}
	trySandboxTTL, err := getEnvDuration("TRY_SANDBOX_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	adminEmail, err := validateBootstrapAdminCredentials(getEnv("ADMIN_EMAIL", ""), getEnv("ADMIN_PASSWORD", ""))
	if err != nil {
		return Config{}, err
	}
	githubClientID := strings.TrimSpace(getEnv("GITHUB_CLIENT_ID", ""))
	githubClientSecret := strings.TrimSpace(getEnv("GITHUB_CLIENT_SECRET", ""))
	googleClientID := strings.TrimSpace(getEnv("GOOGLE_CLIENT_ID", ""))
	googleClientSecret := strings.TrimSpace(getEnv("GOOGLE_CLIENT_SECRET", ""))
	if err := validateOAuthPair("GITHUB", githubClientID, githubClientSecret); err != nil {
		return Config{}, err
	}
	if err := validateOAuthPair("GOOGLE", googleClientID, googleClientSecret); err != nil {
		return Config{}, err
	}
	loadedConfig := Config{
		Port:                getEnv("PORT", "4000"),
		MongoURI:            getEnv("MONGO_URI", "mongodb://localhost:27017"),
		Database:            getEnv("MONGO_DB", "app_db"),
		JWTSecret:           getEnv("JWT_SECRET", ""),
		JWTIssuer:           getEnv("JWT_ISSUER", "m-stash"),
		JWTAudience:         getEnv("JWT_AUDIENCE", "m-stash"),
		TrustProxy:          trustProxy,
		MetricsToken:        getEnv("METRICS_TOKEN", ""),
		AllowedOrigins:      getAllowedOrigins(),
		AccessTokenTTL:      accessTokenTTL,
		RefreshSessionTTL:   refreshSessionTTL,
		SessionCookieSecure: sessionCookieSecure,
		PlatformToken:       getEnv("PLATFORM_TOKEN", ""),
		UIAppKey:            getEnv("UI_APP_KEY", ""),
		DefaultAppSlug:      getEnv("DEFAULT_APP_SLUG", "m-stash"),
		DefaultAppName:      getEnv("DEFAULT_APP_NAME", "m-stash"),
		DemoSeed:            demoSeed,
		WebhookDelivery:     webhookDelivery,
		WebhookPollInterval: webhookPollInterval,
		WebhookMaxAttempts:  webhookMaxAttempts,
		GeminiAPIKey:        getEnv("GEMINI_API_KEY", ""),
		GeminiModel:         getEnv("GEMINI_MODEL", "gemini-3.5-flash-lite"),
		VibeDailyLimit:      vibeDailyLimit,
		VibeShareTTL:        vibeShareTTL,
		VibeMaxSandboxes:    vibeMaxSandboxes,
		PublicBaseURL:       getEnv("PUBLIC_BASE_URL", ""),

		GitHubClientID:     githubClientID,
		GitHubClientSecret: githubClientSecret,
		GoogleClientID:     googleClientID,
		GoogleClientSecret: googleClientSecret,

		BuilderSignupOpen:       builderSignupOpen,
		BuilderSignupDailyLimit: builderSignupDailyLimit,
		FreeTierMaxApps:         freeTierMaxApps,
		FreeTierMaxUsers:        freeTierMaxUsers,
		FreeTierMaxRecords:      freeTierMaxRecords,
		FreeTierMaxRecordBytes:  freeTierMaxRecordBytes,
		TrySandbox:              trySandbox,
		TrySandboxTTL:           trySandboxTTL,
		AdminEmail:              adminEmail,
		AdminPassword:           getEnv("ADMIN_PASSWORD", ""),
	}

	if len(loadedConfig.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if loadedConfig.MetricsToken != "" && len(loadedConfig.MetricsToken) < 32 {
		return Config{}, errors.New("METRICS_TOKEN must be at least 32 characters when configured")
	}
	if len(loadedConfig.PlatformToken) < 32 {
		return Config{}, errors.New("PLATFORM_TOKEN must be at least 32 characters")
	}
	if loadedConfig.JWTIssuer == "" || loadedConfig.JWTAudience == "" {
		return Config{}, errors.New("JWT_ISSUER and JWT_AUDIENCE must be configured")
	}
	if loadedConfig.WebhookMaxAttempts < 1 || loadedConfig.WebhookMaxAttempts > 100 {
		return Config{}, errors.New("WEBHOOK_MAX_ATTEMPTS must be between 1 and 100")
	}
	return loadedConfig, nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// getEnvBool reads a boolean flag. It accepts the Go literals true/false (and
// 1/0/t/f) plus the friendly on/off and yes/no vocabulary the docs, compose
// file, and /admin toggles use, so DEMO_DATA=on no longer crash-loops the app.
func getEnvBool(key string, fallback bool) (bool, error) {
	rawValue, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(rawValue) == "" {
		return fallback, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(rawValue))
	switch normalized {
	case "on", "yes":
		return true, nil
	case "off", "no":
		return false, nil
	}
	value, err := strconv.ParseBool(normalized)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (true/false, on/off, or yes/no)", key)
	}
	return value, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	rawValue, exists := os.LookupEnv(key)
	if !exists || rawValue == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(rawValue)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", key)
	}
	return value, nil
}

// getEnvToggle reads a two-valued flag such as BUILDER_SIGNUP=open|off, returning
// true for onValue and false for offValue. An empty or unset variable uses the
// fallback; anything else is a configuration error.
func getEnvToggle(key, onValue, offValue string, fallback bool) (bool, error) {
	rawValue, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(rawValue) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(rawValue)) {
	case onValue:
		return true, nil
	case offValue:
		return false, nil
	default:
		return false, fmt.Errorf("%s must be %s or %s", key, onValue, offValue)
	}
}

func getEnvInt(key string, fallback int) (int, error) {
	rawValue, exists := os.LookupEnv(key)
	if !exists || rawValue == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}

func validateBootstrapAdminCredentials(email, password string) (string, error) {
	email = normalizeEmail(email)
	if email == "" && password == "" {
		return "", nil
	}
	if email == "" || password == "" {
		return "", errors.New("ADMIN_EMAIL and ADMIN_PASSWORD must be configured together")
	}
	if err := validateSignUpCredentials(email, password); err != nil {
		return "", fmt.Errorf("invalid bootstrap admin credentials: %w", err)
	}
	return email, nil
}

// validateOAuthPair ensures an OAuth provider is either fully configured (both
// client id and secret) or fully absent; a half-configured provider is a
// misconfiguration that would silently disable social login.
func validateOAuthPair(provider, clientID, clientSecret string) error {
	if (clientID == "") != (clientSecret == "") {
		return fmt.Errorf("%s_CLIENT_ID and %s_CLIENT_SECRET must be set together", provider, provider)
	}
	return nil
}

func getAllowedOrigins() []string {
	rawOrigins := strings.TrimSpace(getEnv("ALLOWED_ORIGINS", ""))
	if rawOrigins == "" {
		return []string{"*"}
	}
	origins := make([]string, 0)
	for _, rawOrigin := range strings.Split(rawOrigins, ",") {
		if origin := strings.TrimSpace(rawOrigin); origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return []string{"*"}
	}
	return origins
}

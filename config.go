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
	WebhookDelivery     bool
	WebhookPollInterval time.Duration
	WebhookMaxAttempts  int
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
		WebhookDelivery:     webhookDelivery,
		WebhookPollInterval: webhookPollInterval,
		WebhookMaxAttempts:  webhookMaxAttempts,
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

func getEnvBool(key string, fallback bool) (bool, error) {
	rawValue, exists := os.LookupEnv(key)
	if !exists || rawValue == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(rawValue)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
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

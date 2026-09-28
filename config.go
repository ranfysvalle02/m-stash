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
	AdminEmail          string
	AdminPassword       string
	ServiceToken        string
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
	adminEmail, err := validateBootstrapAdminCredentials(
		getEnv("ADMIN_EMAIL", ""),
		getEnv("ADMIN_PASSWORD", ""),
	)
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
		AdminEmail:          adminEmail,
		AdminPassword:       getEnv("ADMIN_PASSWORD", ""),
		ServiceToken:        getEnv("SERVICE_TOKEN", ""),
	}

	if len(loadedConfig.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if loadedConfig.MetricsToken != "" && len(loadedConfig.MetricsToken) < 32 {
		return Config{}, errors.New("METRICS_TOKEN must be at least 32 characters when configured")
	}
	if loadedConfig.ServiceToken != "" && len(loadedConfig.ServiceToken) < 32 {
		return Config{}, errors.New("SERVICE_TOKEN must be at least 32 characters when configured")
	}
	if loadedConfig.JWTIssuer == "" || loadedConfig.JWTAudience == "" {
		return Config{}, errors.New("JWT_ISSUER and JWT_AUDIENCE must be configured")
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

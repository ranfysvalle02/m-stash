package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type CollectionRule struct {
	Read                  any      `json:"read"`
	Write                 any      `json:"write"`
	AllowedWriteFields    []string `json:"allowed_write_fields"`
	RestrictedWriteFields []string `json:"restricted_write_fields"`
}

type Config struct {
	Port                string                    `json:"port"`
	MongoURI            string                    `json:"mongo_uri"`
	Database            string                    `json:"database"`
	JWTSecret           string                    `json:"jwt_secret"`
	JWTIssuer           string                    `json:"jwt_issuer"`
	JWTAudience         string                    `json:"jwt_audience"`
	TrustProxy          bool                      `json:"trust_proxy"`
	MetricsToken        string                    `json:"-"`
	AllowedOrigins      []string                  `json:"allowed_origins"`
	Rules               map[string]CollectionRule `json:"rules"`
	AccessTokenTTL      time.Duration             `json:"-"`
	RefreshSessionTTL   time.Duration             `json:"-"`
	SessionCookieSecure bool                      `json:"-"`
	AdminEmail          string                    `json:"-"`
	AdminPassword       string                    `json:"-"`
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
	adminEmail, err := validateBootstrapAdminCredentials(getEnv("ADMIN_EMAIL", ""), getEnv("ADMIN_PASSWORD", ""))
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
		Rules: map[string]CollectionRule{
			"stashes": {
				Read:                  map[string]any{"$or": []any{map[string]any{"ownerId": "$auth.uid"}, map[string]any{"isPublic": true}}},
				Write:                 map[string]any{"ownerId": "$auth.uid"},
				AllowedWriteFields:    []string{"ownerId", "title", "summary", "content", "tags", "isPublic"},
				RestrictedWriteFields: []string{"role", "isVerified", "createdAt", "updatedAt"},
			},
			"profiles": {
				Read:                  map[string]any{"_id": "$auth.uid"},
				Write:                 map[string]any{"_id": "$auth.uid"},
				AllowedWriteFields:    []string{"_id", "handle", "displayName", "bio", "avatarURL", "links", "isPublic"},
				RestrictedWriteFields: []string{"role", "permissions", "email"},
			},
		},
	}
	configFile := getEnv("M_STASH_CONFIG", "gateway.json")
	if _, err := os.Stat(configFile); err == nil {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return Config{}, fmt.Errorf("read configuration file %q: %w", configFile, err)
		}
		var fileConfig Config
		if err := json.Unmarshal(data, &fileConfig); err != nil {
			return Config{}, fmt.Errorf("parse configuration file %q: %w", configFile, err)
		}
		if fileConfig.Port != "" {
			loadedConfig.Port = fileConfig.Port
		}
		if fileConfig.MongoURI != "" {
			loadedConfig.MongoURI = fileConfig.MongoURI
		}
		if fileConfig.Database != "" {
			loadedConfig.Database = fileConfig.Database
		}
		if fileConfig.JWTSecret != "" {
			loadedConfig.JWTSecret = fileConfig.JWTSecret
		}
		if fileConfig.JWTIssuer != "" {
			loadedConfig.JWTIssuer = fileConfig.JWTIssuer
		}
		if fileConfig.JWTAudience != "" {
			loadedConfig.JWTAudience = fileConfig.JWTAudience
		}
		if len(fileConfig.AllowedOrigins) > 0 {
			loadedConfig.AllowedOrigins = fileConfig.AllowedOrigins
		}
		if len(fileConfig.Rules) > 0 {
			loadedConfig.Rules = fileConfig.Rules
		}
		log.Printf("Loaded configuration from %s", configFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("check configuration file %q: %w", configFile, err)
	}

	if len(loadedConfig.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if loadedConfig.MetricsToken != "" && len(loadedConfig.MetricsToken) < 32 {
		return Config{}, errors.New("METRICS_TOKEN must be at least 32 characters when configured")
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
	origins := strings.Split(getEnv("ALLOWED_ORIGINS", "*"), ",")
	for index := range origins {
		origins[index] = strings.TrimSpace(origins[index])
	}
	return origins
}

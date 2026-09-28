package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type CollectionRule struct {
	Read                  any      `json:"read"`
	Write                 any      `json:"write"`
	AllowedWriteFields    []string `json:"allowed_write_fields"`
	RestrictedWriteFields []string `json:"restricted_write_fields"`
}

type Config struct {
	Port           string                    `json:"port"`
	MongoURI       string                    `json:"mongo_uri"`
	Database       string                    `json:"database"`
	JWTSecret      string                    `json:"jwt_secret"`
	JWTIssuer      string                    `json:"jwt_issuer"`
	JWTAudience    string                    `json:"jwt_audience"`
	TrustProxy     bool                      `json:"trust_proxy"`
	MetricsToken   string                    `json:"-"`
	AllowedOrigins []string                  `json:"allowed_origins"`
	Rules          map[string]CollectionRule `json:"rules"`
}

func loadConfig() (Config, error) {
	trustProxy, err := getEnvBool("TRUST_PROXY", false)
	if err != nil {
		return Config{}, err
	}
	loadedConfig := Config{
		Port:           getEnv("PORT", "4000"),
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		Database:       getEnv("MONGO_DB", "app_db"),
		JWTSecret:      getEnv("JWT_SECRET", ""),
		JWTIssuer:      getEnv("JWT_ISSUER", "m-stash"),
		JWTAudience:    getEnv("JWT_AUDIENCE", "m-stash"),
		TrustProxy:     trustProxy,
		MetricsToken:   getEnv("METRICS_TOKEN", ""),
		AllowedOrigins: getAllowedOrigins(),
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

func getAllowedOrigins() []string {
	origins := strings.Split(getEnv("ALLOWED_ORIGINS", "*"), ",")
	for index := range origins {
		origins[index] = strings.TrimSpace(origins[index])
	}
	return origins
}

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
	"nhooyr.io/websocket"
)

// ============================================================================
// CONFIGURATION & GLOBAL STATE
// ============================================================================

type CollectionRule struct {
	Read                  any      `json:"read"`                    // Rule AST or boolean
	Write                 any      `json:"write"`                   // Rule AST or boolean
	AllowedWriteFields    []string `json:"allowed_write_fields"`    // Optional whitelist mask
	RestrictedWriteFields []string `json:"restricted_write_fields"` // Blacklisted schema fields (e.g. "role")
}

const (
	defaultPublicPageSize = 20
	maxPublicPageSize     = 100
	defaultQueryPageSize  = 50
	maxQueryPageSize      = 100
)

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

var (
	config      Config
	db          *mongo.Database
	mongoClient *mongo.Client
)

const maxPayloadBytes = 1024 * 1024 // 1MB Request Payload Limit

const bcryptCost = 12

type Claims struct {
	UID   string `json:"uid"`
	Email string `json:"email"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

type User struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Email        string        `bson:"email" json:"email"`
	PasswordHash string        `bson:"password" json:"-"`
	Role         string        `bson:"role" json:"role"`
	CreatedAt    time.Time     `bson:"createdAt" json:"createdAt"`
}

type publicStash struct {
	ID        bson.ObjectID `bson:"_id" json:"_id"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Content   string        `bson:"content,omitempty" json:"content,omitempty"`
	Tags      []string      `bson:"tags,omitempty" json:"tags,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type publicStashPreview struct {
	ID        bson.ObjectID `bson:"_id" json:"_id"`
	Title     string        `bson:"title" json:"title"`
	Summary   string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Tags      []string      `bson:"tags,omitempty" json:"tags,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type publicStashCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

// ============================================================================
// CONFIGURATION LOADER
// ============================================================================

func loadConfig() error {
	trustProxy, err := getEnvBool("TRUST_PROXY", false)
	if err != nil {
		return err
	}
	config = Config{
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
			return fmt.Errorf("read configuration file %q: %w", configFile, err)
		}
		var fileConfig Config
		if err := json.Unmarshal(data, &fileConfig); err != nil {
			return fmt.Errorf("parse configuration file %q: %w", configFile, err)
		}
		if fileConfig.Port != "" {
			config.Port = fileConfig.Port
		}
		if fileConfig.MongoURI != "" {
			config.MongoURI = fileConfig.MongoURI
		}
		if fileConfig.Database != "" {
			config.Database = fileConfig.Database
		}
		if fileConfig.JWTSecret != "" {
			config.JWTSecret = fileConfig.JWTSecret
		}
		if fileConfig.JWTIssuer != "" {
			config.JWTIssuer = fileConfig.JWTIssuer
		}
		if fileConfig.JWTAudience != "" {
			config.JWTAudience = fileConfig.JWTAudience
		}
		if len(fileConfig.AllowedOrigins) > 0 {
			config.AllowedOrigins = fileConfig.AllowedOrigins
		}
		if len(fileConfig.Rules) > 0 {
			config.Rules = fileConfig.Rules
		}
		log.Printf("Loaded configuration from %s", configFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check configuration file %q: %w", configFile, err)
	}

	if len(config.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 characters")
	}
	if config.MetricsToken != "" && len(config.MetricsToken) < 32 {
		return errors.New("METRICS_TOKEN must be at least 32 characters when configured")
	}
	if config.JWTIssuer == "" || config.JWTAudience == "" {
		return errors.New("JWT_ISSUER and JWT_AUDIENCE must be configured")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
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

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateSignUpCredentials(email, password string) error {
	if len(email) == 0 || len(email) > 254 {
		return errors.New("a valid email is required")
	}
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return errors.New("a valid email is required")
	}
	if len(password) < 12 || len(password) > 72 {
		return errors.New("password must be between 12 and 72 characters")
	}
	return nil
}

// ============================================================================
// TYPE NORMALIZATION & ACCESS CONTROL ENGINE
// ============================================================================

// Returns true strictly if the field key denotes an identifier
func isIDField(key string) bool {
	if key == "" || strings.HasPrefix(key, "$") {
		return false
	}
	return key == "_id" || strings.HasSuffix(key, "Id") || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "ID")
}

// Parses string to ObjectID ONLY when context is an explicit ID field
func parseObjectID(v string, keyContext string) any {
	if isIDField(keyContext) && len(v) == 24 {
		if objID, err := bson.ObjectIDFromHex(v); err == nil {
			return objID
		}
	}
	return v
}

// Interpolates claims & preserves keyContext across Mongo query operators ($in, $ne,$or, etc.)
func interpolateAndNormalize(node any, claims *Claims, keyContext string) any {
	switch v := node.(type) {
	case string:
		var interpolated string
		switch v {
		case "$auth.uid":
			interpolated = claims.UID
		case "$auth.email":
			interpolated = claims.Email
		case "$auth.role":
			interpolated = claims.Role
		default:
			interpolated = strings.ReplaceAll(v, "$auth.uid", claims.UID)
			interpolated = strings.ReplaceAll(interpolated, "$auth.role", claims.Role)
			interpolated = strings.ReplaceAll(interpolated, "$auth.email", claims.Email)
		}
		return parseObjectID(interpolated, keyContext)

	case map[string]any:
		res := make(map[string]any)
		for k, val := range v {
			nextContext := k
			if strings.HasPrefix(k, "$") {
				// Preserve parent field context for query operators (e.g. {"_id": {"$in": [...]}})
				nextContext = keyContext
			}
			res[k] = interpolateAndNormalize(val, claims, nextContext)
		}
		return res

	case []any:
		res := make([]any, len(v))
		for i, val := range v {
			res[i] = interpolateAndNormalize(val, claims, keyContext)
		}
		return res

	default:
		return v
	}
}

// Normalizes client query values while maintaining operator keyContext
func normalizeClientQuery(node any, keyContext string) any {
	switch v := node.(type) {
	case string:
		return parseObjectID(v, keyContext)
	case map[string]any:
		res := make(map[string]any)
		for k, val := range v {
			nextContext := k
			if strings.HasPrefix(k, "$") {
				nextContext = keyContext
			}
			res[k] = normalizeClientQuery(val, nextContext)
		}
		return res
	case []any:
		res := make([]any, len(v))
		for i, val := range v {
			res[i] = normalizeClientQuery(val, keyContext)
		}
		return res
	default:
		return v
	}
}

var allowedClientQueryOperators = map[string]bool{
	"$and":    true,
	"$or":     true,
	"$eq":     true,
	"$ne":     true,
	"$in":     true,
	"$nin":    true,
	"$gt":     true,
	"$gte":    true,
	"$lt":     true,
	"$lte":    true,
	"$exists": true,
	"$all":    true,
}

func validateClientQuery(node any, depth int) error {
	if depth > 16 {
		return errors.New("query nesting exceeds the maximum depth")
	}

	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.ContainsRune(key, '\x00') {
				return errors.New("query contains an invalid field name")
			}
			if strings.HasPrefix(key, "$") && !allowedClientQueryOperators[key] {
				return fmt.Errorf("query operator %q is not allowed", key)
			}
			if key == "$and" || key == "$or" {
				if _, ok := child.([]any); !ok {
					return fmt.Errorf("query operator %q requires an array", key)
				}
			}
			if err := validateClientQuery(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := validateClientQuery(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateQueryLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultQueryPageSize, nil
	}
	if limit < 1 || limit > maxQueryPageSize {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxQueryPageSize)
	}
	return limit, nil
}

func valuesEqual(v1, v2 any) bool {
	if reflect.DeepEqual(v1, v2) {
		return true
	}

	str1 := fmt.Sprintf("%v", v1)
	str2 := fmt.Sprintf("%v", v2)

	if str1 == str2 {
		return true
	}

	if objID1, ok := v1.(bson.ObjectID); ok {
		return objID1.Hex() == str2
	}
	if objID2, ok := v2.(bson.ObjectID); ok {
		return objID2.Hex() == str1
	}

	return false
}

// Validates schema field write permissions against whitelists and blacklists
func validateFieldWriteMask(payload map[string]any, rule CollectionRule) error {
	if payload == nil {
		return nil
	}

	// 1. Check blacklisted fields
	for _, restricted := range rule.RestrictedWriteFields {
		if _, exists := payload[restricted]; exists {
			return fmt.Errorf("field '%s' is restricted and cannot be written by clients", restricted)
		}
	}

	// 2. Check whitelisted fields (if whitelist is configured)
	if len(rule.AllowedWriteFields) > 0 {
		allowedMap := make(map[string]bool)
		for _, field := range rule.AllowedWriteFields {
			allowedMap[field] = true
		}
		for field := range payload {
			if !allowedMap[field] {
				return fmt.Errorf("field '%s' is not in the allowed write schema", field)
			}
		}
	}

	return nil
}

func applySecurityFilter(clientQuery map[string]any, rawRule any, claims *Claims) (bson.M, error) {
	if rawRule == nil {
		return nil, fmt.Errorf("permission denied: no read policy configured")
	}
	if err := validateClientQuery(clientQuery, 0); err != nil {
		return nil, err
	}

	normalizedClientQuery, _ := normalizeClientQuery(clientQuery, "").(map[string]any)

	if allowed, ok := rawRule.(bool); ok {
		if !allowed {
			return nil, fmt.Errorf("permission denied: read operation prohibited")
		}
		if normalizedClientQuery == nil {
			return bson.M{}, nil
		}
		return bson.M(normalizedClientQuery), nil
	}

	interpolated := interpolateAndNormalize(rawRule, claims, "")
	ruleMap, ok := interpolated.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid security rule structure")
	}

	if len(normalizedClientQuery) == 0 {
		return bson.M(ruleMap), nil
	}

	return bson.M{
		"$and": []any{normalizedClientQuery, ruleMap},
	}, nil
}

func authorizeAndHydrateInsert(payload map[string]any, rule CollectionRule, claims *Claims) (map[string]any, error) {
	if rule.Write == nil {
		return nil, fmt.Errorf("write access denied: no write policy configured")
	}

	if payload == nil {
		payload = make(map[string]any)
	}

	// Field-level schema mask check
	if err := validateFieldWriteMask(payload, rule); err != nil {
		return nil, err
	}

	if allowed, ok := rule.Write.(bool); ok {
		if !allowed {
			return nil, fmt.Errorf("write access denied by policy")
		}
		return payload, nil
	}

	ruleMap, ok := rule.Write.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid write rule configuration")
	}

	interpolatedRule, _ := interpolateAndNormalize(ruleMap, claims, "").(map[string]any)

	for k, expectedVal := range interpolatedRule {
		if strings.HasPrefix(k, "$") {
			return nil, fmt.Errorf("complex query operators (%s) are not supported in insert write rules", k)
		}

		existingVal, exists := payload[k]
		if !exists {
			payload[k] = expectedVal
		} else {
			if !valuesEqual(existingVal, expectedVal) {
				return nil, fmt.Errorf("payload field '%s' violates security rule policy", k)
			}
		}
	}

	return payload, nil
}

func sanitizeUpdatePayload(payload map[string]any, rule CollectionRule, claims *Claims) (map[string]any, error) {
	if payload == nil {
		return nil, fmt.Errorf("update payload cannot be empty")
	}

	delete(payload, "_id")

	// Field-level schema mask check
	if err := validateFieldWriteMask(payload, rule); err != nil {
		return nil, err
	}

	if rule.Write == nil {
		return nil, fmt.Errorf("write access denied: no write policy configured")
	}

	if allowed, ok := rule.Write.(bool); ok {
		if !allowed {
			return nil, fmt.Errorf("write access denied by policy")
		}
		return payload, nil
	}

	ruleMap, ok := rule.Write.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid write rule configuration")
	}

	interpolatedRule, _ := interpolateAndNormalize(ruleMap, claims, "").(map[string]any)

	for k, expectedVal := range interpolatedRule {
		if strings.HasPrefix(k, "$") {
			continue
		}

		if existingVal, exists := payload[k]; exists {
			if !valuesEqual(existingVal, expectedVal) {
				return nil, fmt.Errorf("cannot modify restricted field '%s' to a value violating policy", k)
			}
		}
	}

	return payload, nil
}

func validateProfilePayload(payload map[string]any) error {
	handle, exists := payload["handle"]
	if !exists {
		return nil
	}
	handleString, ok := handle.(string)
	if !ok || !isProfileHandle(handleString) {
		return fmt.Errorf("profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
	}
	return nil
}

func validateStashPayload(payload map[string]any) error {
	title, exists := payload["title"]
	if !exists {
		return fmt.Errorf("stash title is required")
	}
	titleString, ok := title.(string)
	if !ok || strings.TrimSpace(titleString) == "" || len(titleString) > 200 {
		return fmt.Errorf("stash title must be a non-empty string up to 200 characters")
	}
	return nil
}

// ============================================================================
// HTTP MIDDLEWARE & HANDLERS
// ============================================================================

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false

		if origin != "" {
			for _, o := range config.AllowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					w.Header().Set("Access-Control-Allow-Origin", origin)
					if o != "*" {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					break
				}
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			if !allowed && origin != "" && len(config.AllowedOrigins) > 0 && config.AllowedOrigins[0] != "*" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowedOrigin := range config.AllowedOrigins {
		if allowedOrigin == "*" || allowedOrigin == origin {
			return true
		}
	}
	return false
}

func authenticate(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
			}
			return []byte(config.JWTSecret), nil
		},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithIssuer(config.JWTIssuer),
			jwt.WithAudience(config.JWTAudience),
			jwt.WithLeeway(30*time.Second),
		)

		if err != nil || !token.Valid || claims.UID == "" || claims.Subject != claims.UID {
			writeError(w, http.StatusForbidden, "Invalid or expired token")
			return
		}

		next(w, r, claims)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Failed to write JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// requireMethod writes a 405 and returns false when the request method doesn't match.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return false
	}
	return true
}

// decodeJSONBody enforces the payload size limit and decodes JSON, writing an error response on failure.
// allowEmptyBody permits an empty body (io.EOF) to pass through unchanged.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, allowEmptyBody bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)

	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil {
		return true
	}
	if allowEmptyBody && errors.Is(err, io.EOF) {
		return true
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "Request body exceeds maximum limit (1MB)")
		return false
	}
	writeError(w, http.StatusBadRequest, "Malformed JSON request body: "+err.Error())
	return false
}

func handleSignUp(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &req, false) {
		return
	}
	req.Email = normalizeEmail(req.Email)
	if err := validateSignUpCredentials(req.Email, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	usersCol := db.Collection("_users")
	count, err := usersCol.CountDocuments(ctx, bson.M{"email": req.Email})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database query failed during user check")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "User already exists")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error hashing password")
		return
	}

	newUser := User{
		ID:           bson.NewObjectID(),
		Email:        req.Email,
		PasswordHash: string(hash),
		Role:         "user",
		CreatedAt:    time.Now(),
	}

	_, err = usersCol.InsertOne(ctx, newUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}

	token, err := generateJWT(newUser.ID.Hex(), newUser.Email, newUser.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "user": newUser})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &req, false) {
		return
	}
	req.Email = normalizeEmail(req.Email)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var user User
	err := db.Collection("_users").FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	token, err := generateJWT(user.ID.Hex(), user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func handleTokenVerification(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active": true,
		"claims": map[string]any{
			"sub":       claims.Subject,
			"uid":       claims.UID,
			"email":     claims.Email,
			"role":      claims.Role,
			"iss":       claims.Issuer,
			"aud":       claims.Audience,
			"jti":       claims.ID,
			"issuedAt":  claims.IssuedAt,
			"expiresAt": claims.ExpiresAt,
		},
	})
}

func handleServiceManifest(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": "v1",
		"service":    "m-stash",
		"authentication": map[string]any{
			"scheme":               "Bearer",
			"verificationEndpoint": "/v1/auth/verify",
			"issuer":               config.JWTIssuer,
			"audience":             config.JWTAudience,
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
				"enabled":  config.MetricsToken != "",
				"endpoint": "/metrics",
			},
		},
	})
}

func handlePublicProfile(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	pathParts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/public/profiles/"), "/"), "/")
	if len(pathParts) == 2 && pathParts[1] == "stashes" {
		handlePublicStashes(w, r, pathParts[0])
		return
	}
	if len(pathParts) != 1 {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	handle := pathParts[0]
	if !isProfileHandle(handle) {
		writeError(w, http.StatusBadRequest, "Profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var profile bson.M
	err := db.Collection("profiles").FindOne(
		ctx,
		bson.M{"handle": handle, "isPublic": true},
		options.FindOne().SetProjection(bson.M{
			"_id":         1,
			"handle":      1,
			"displayName": 1,
			"bio":         1,
			"avatarURL":   1,
			"links":       1,
		}),
	).Decode(&profile)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public profile")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": profile})
}

func handlePublicStashes(w http.ResponseWriter, r *http.Request, handle string) {
	if !isProfileHandle(handle) {
		writeError(w, http.StatusBadRequest, "Profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
		return
	}
	pageSize, pageCursor, err := parsePublicStashPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if len(tag) > 64 {
		writeError(w, http.StatusBadRequest, "tag must be 64 characters or fewer")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var profile bson.M
	err = db.Collection("profiles").FindOne(
		ctx,
		bson.M{"handle": handle, "isPublic": true},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Decode(&profile)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public profile")
		return
	}

	filter := bson.M{"ownerId": profile["_id"], "isPublic": true}
	if tag != "" {
		filter["tags"] = tag
	}
	if pageCursor != nil {
		cursorFilter, err := publicStashCursorFilter(pageCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		filter["$or"] = cursorFilter["$or"]
	}

	cursor, err := db.Collection("stashes").Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(pageSize+1)).SetProjection(bson.M{
			"_id":       1,
			"title":     1,
			"summary":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load public stashes")
		return
	}
	defer cursor.Close(ctx)

	var stashes []publicStashPreview
	if err := cursor.All(ctx, &stashes); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode public stashes")
		return
	}

	var nextCursor any
	if len(stashes) > pageSize {
		last := stashes[pageSize-1]
		nextCursor = encodePublicStashCursor(last.CreatedAt, last.ID)
		stashes = stashes[:pageSize]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": stashes,
		"page": map[string]any{
			"limit":      pageSize,
			"nextCursor": nextCursor,
		},
	})
}

func handlePublicDiscovery(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	stashID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/public/stashes"), "/")
	if stashID != "" {
		handlePublicStashDetail(w, r, stashID)
		return
	}

	pageSize, pageCursor, err := parsePublicStashPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if len(tag) > 64 {
		writeError(w, http.StatusBadRequest, "tag must be 64 characters or fewer")
		return
	}

	filter := bson.M{"isPublic": true}
	if tag != "" {
		filter["tags"] = tag
	}
	if pageCursor != nil {
		cursorFilter, err := publicStashCursorFilter(pageCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		filter["$or"] = cursorFilter["$or"]
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cursor, err := db.Collection("stashes").Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(pageSize+1)).SetProjection(bson.M{
			"_id":       1,
			"title":     1,
			"summary":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load public discovery feed")
		return
	}
	defer cursor.Close(ctx)

	var stashes []publicStashPreview
	if err := cursor.All(ctx, &stashes); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode public discovery feed")
		return
	}

	var nextCursor any
	if len(stashes) > pageSize {
		last := stashes[pageSize-1]
		nextCursor = encodePublicStashCursor(last.CreatedAt, last.ID)
		stashes = stashes[:pageSize]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": stashes,
		"page": map[string]any{
			"limit":      pageSize,
			"nextCursor": nextCursor,
		},
	})
}

func handlePublicStashDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	if strings.Contains(rawID, "/") {
		writeError(w, http.StatusNotFound, "Public resource not found")
		return
	}
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Stash id must be a valid ObjectID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var stash publicStash
	err = db.Collection("stashes").FindOne(
		ctx,
		bson.M{"_id": id, "isPublic": true},
		options.FindOne().SetProjection(bson.M{
			"_id":       1,
			"title":     1,
			"summary":   1,
			"content":   1,
			"tags":      1,
			"createdAt": 1,
			"updatedAt": 1,
		}),
	).Decode(&stash)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "Public stash not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load public stash")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": stash})
}

func parsePublicStashPage(r *http.Request) (int, *publicStashCursor, error) {
	pageSize := defaultPublicPageSize
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > maxPublicPageSize {
			return 0, nil, fmt.Errorf("limit must be an integer between 1 and %d", maxPublicPageSize)
		}
		pageSize = parsedLimit
	}

	rawCursor := r.URL.Query().Get("cursor")
	if rawCursor == "" {
		return pageSize, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil {
		return 0, nil, errors.New("cursor is invalid")
	}
	var cursor publicStashCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() {
		return 0, nil, errors.New("cursor is invalid")
	}
	if _, err := bson.ObjectIDFromHex(cursor.ID); err != nil {
		return 0, nil, errors.New("cursor is invalid")
	}
	return pageSize, &cursor, nil
}

func encodePublicStashCursor(createdAt time.Time, id bson.ObjectID) string {
	payload, _ := json.Marshal(publicStashCursor{CreatedAt: createdAt, ID: id.Hex()})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func publicStashCursorFilter(cursor *publicStashCursor) (bson.M, error) {
	objectID, err := bson.ObjectIDFromHex(cursor.ID)
	if err != nil {
		return nil, err
	}
	return bson.M{"$or": bson.A{
		bson.M{"createdAt": bson.M{"$lt": cursor.CreatedAt}},
		bson.M{"createdAt": cursor.CreatedAt, "_id": bson.M{"$lt": objectID}},
	}}, nil
}

func isProfileHandle(handle string) bool {
	if len(handle) < 3 || len(handle) > 32 {
		return false
	}
	for _, character := range handle {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func generateJWT(uid, email, role string) (string, error) {
	claims := Claims{
		UID:   uid,
		Email: email,
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.JWTIssuer,
			Subject:   uid,
			Audience:  jwt.ClaimStrings{config.JWTAudience},
			ID:        bson.NewObjectID().Hex(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.JWTSecret))
}

func handleDatabaseProxy(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "Invalid endpoint path. Use /v1/db/{collection}/{action}")
		return
	}
	collectionName, action := parts[2], parts[3]
	if collectionName == outboxCollectionName || strings.HasPrefix(collectionName, "_m_stash_") {
		writeError(w, http.StatusForbidden, "Access to internal collections is restricted")
		return
	}

	rule, exists := config.Rules[collectionName]
	if !exists {
		writeError(w, http.StatusForbidden, fmt.Sprintf("Access to collection '%s' is restricted", collectionName))
		return
	}

	var body struct {
		Query   map[string]any `json:"query"`
		Payload map[string]any `json:"payload"`
		Limit   int            `json:"limit"`
	}
	if !decodeJSONBody(w, r, &body, true) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	coll := db.Collection(collectionName)

	switch action {
	case "find":
		limit, err := validateQueryLimit(body.Limit)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		cursor, err := coll.Find(ctx, filter, options.Find().SetLimit(int64(limit)))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var results []bson.M
		if err := cursor.All(ctx, &results); err != nil {
			writeError(w, http.StatusInternalServerError, "Error decoding cursor results: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": results,
			"page": map[string]int{"limit": limit},
		})

	case "findOne":
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		var result bson.M
		err = coll.FindOne(ctx, filter).Decode(&result)
		if err != nil {
			writeError(w, http.StatusNotFound, "Document not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case "insertOne":
		validatedPayload, err := authorizeAndHydrateInsert(body.Payload, rule, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if collectionName == "profiles" {
			if err := validateProfilePayload(validatedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if collectionName == "stashes" {
			if err := validateStashPayload(validatedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			validatedPayload["createdAt"] = time.Now().UTC()
			validatedPayload["updatedAt"] = time.Now().UTC()
		}

		res, err := executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			result, err := coll.InsertOne(transactionContext, validatedPayload)
			if err != nil {
				return mutationOutcome{}, err
			}
			return mutationOutcome{
				Result: result,
				Event: &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: objectIDString(result.InsertedID),
					Data:       bson.M{"fields": fieldNames(validatedPayload)},
				},
			}, nil
		})
		if err != nil {
			logger.Error("database insert failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not create document")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": res})

	case "updateOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}

		sanitizedPayload, err := sanitizeUpdatePayload(body.Payload, rule, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if collectionName == "profiles" {
			if err := validateProfilePayload(sanitizedPayload); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if collectionName == "stashes" {
			if title, exists := sanitizedPayload["title"]; exists {
				if err := validateStashPayload(map[string]any{"title": title}); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			sanitizedPayload["updatedAt"] = time.Now().UTC()
		}

		res, err := executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			var target struct {
				ID bson.ObjectID `bson:"_id"`
			}
			lookupErr := coll.FindOne(transactionContext, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&target)
			if lookupErr != nil && !errors.Is(lookupErr, mongo.ErrNoDocuments) {
				return mutationOutcome{}, lookupErr
			}

			result, err := coll.UpdateOne(transactionContext, filter, bson.M{"$set": sanitizedPayload})
			if err != nil {
				return mutationOutcome{}, err
			}
			outcome := mutationOutcome{Result: result}
			if result.MatchedCount > 0 {
				if lookupErr != nil {
					return mutationOutcome{}, errors.New("could not identify updated document for outbox event")
				}
				outcome.Event = &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: target.ID.Hex(),
					Data: bson.M{
						"changedFields": fieldNames(sanitizedPayload),
						"matchedCount":  result.MatchedCount,
						"modifiedCount": result.ModifiedCount,
					},
				}
			}
			return outcome, nil
		})
		if err != nil {
			logger.Error("database update failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not update document")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": res})

	case "deleteOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		res, err := executeMutationWithOutbox(ctx, claims, collectionName, action, func(transactionContext context.Context) (mutationOutcome, error) {
			var target struct {
				ID bson.ObjectID `bson:"_id"`
			}
			lookupErr := coll.FindOne(transactionContext, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&target)
			if lookupErr != nil && !errors.Is(lookupErr, mongo.ErrNoDocuments) {
				return mutationOutcome{}, lookupErr
			}

			result, err := coll.DeleteOne(transactionContext, filter)
			if err != nil {
				return mutationOutcome{}, err
			}
			outcome := mutationOutcome{Result: result}
			if result.DeletedCount > 0 {
				if lookupErr != nil {
					return mutationOutcome{}, errors.New("could not identify deleted document for outbox event")
				}
				outcome.Event = &mutationEvent{
					Type:       mutationEventType(collectionName, action),
					ResourceID: target.ID.Hex(),
					Data:       bson.M{"deletedCount": result.DeletedCount},
				}
			}
			return outcome, nil
		})
		if err != nil {
			logger.Error("database delete failed", "request_id", requestIDFromContext(r.Context()), "collection", collectionName, "error", err)
			writeError(w, http.StatusInternalServerError, "Could not delete document")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": res})

	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported action '%s'", action))
	}
}

type websocketResponseWriter struct {
	header http.Header
	body   bytes.Buffer
}

func newWebsocketResponseWriter() *websocketResponseWriter {
	return &websocketResponseWriter{header: make(http.Header)}
}

func (writer *websocketResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *websocketResponseWriter) Write(body []byte) (int, error) {
	return writer.body.Write(body)
}

func (writer *websocketResponseWriter) WriteHeader(_ int) {}

func handleDatabaseWebSocket(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if !originAllowed(r.Header.Get("Origin")) {
		writeError(w, http.StatusForbidden, "Origin is not allowed")
		return
	}

	collectionName := strings.TrimPrefix(r.URL.Path, "/v1/ws/")
	if collectionName == "" || strings.Contains(collectionName, "/") {
		writeError(w, http.StatusBadRequest, "Use /v1/ws/{collection}")
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	for {
		messageType, message, err := connection.Read(r.Context())
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			_ = connection.Close(websocket.StatusUnsupportedData, "text JSON messages required")
			return
		}

		var request struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(message, &request); err != nil || request.Action == "" || strings.Contains(request.Action, "/") {
			if err := connection.Write(r.Context(), websocket.MessageText, []byte(`{"error":"Messages require a valid action"}`)); err != nil {
				return
			}
			continue
		}

		proxyRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/db/"+collectionName+"/"+request.Action, bytes.NewReader(message))
		if err != nil {
			return
		}
		response := newWebsocketResponseWriter()
		handleDatabaseProxy(response, proxyRequest, claims)
		if err := connection.Write(r.Context(), websocket.MessageText, response.body.Bytes()); err != nil {
			return
		}
	}
}

// ============================================================================
// MAIN ENTRYPOINT
// ============================================================================

func main() {
	if err := loadConfig(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	log.Println("🔌 Connecting to MongoDB...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(config.MongoURI))
	if err != nil {
		log.Fatalf("❌ Failed to connect to Mongo: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("❌ Failed to reach Mongo: %v", err)
	}
	mongoClient = client
	db = client.Database(config.Database)
	if err := verifyTransactionSupport(ctx, mongoClient); err != nil {
		log.Fatalf("MongoDB configuration error: %v", err)
	}
	_, err = db.Collection("_users").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Fatalf("Failed to create user email index: %v", err)
	}
	_, err = db.Collection("profiles").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "handle", Value: 1}},
		Options: options.Index().SetUnique(true).SetSparse(true),
	})
	if err != nil {
		log.Fatalf("❌ Failed to create profile handle index: %v", err)
	}
	_, err = db.Collection("stashes").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "isPublic", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "isPublic", Value: 1}, {Key: "tags", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "isPublic", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "isPublic", Value: 1}, {Key: "tags", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}},
	})
	if err != nil {
		log.Fatalf("❌ Failed to create public stash indexes: %v", err)
	}
	if err := ensureOutboxIndexes(ctx, db); err != nil {
		log.Fatalf("Failed to create outbox indexes: %v", err)
	}
	if err := ensureAuthRateLimitIndexes(ctx, db); err != nil {
		log.Fatalf("Failed to create authentication rate-limit index: %v", err)
	}
	log.Printf("✅ Connected to Mongo database: '%s'", config.Database)
	if config.MetricsToken == "" {
		log.Print("Metrics endpoint disabled because METRICS_TOKEN is not configured")
	}

	authRateLimiter := newAuthRateLimiter(db, 10, 5*time.Minute)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/signup", corsMiddleware(rateLimitMiddleware(authRateLimiter, "signup", handleSignUp)))
	mux.HandleFunc("/v1/auth/login", corsMiddleware(rateLimitMiddleware(authRateLimiter, "login", handleLogin)))
	mux.HandleFunc("/v1/auth/verify", corsMiddleware(authenticate(handleTokenVerification)))
	mux.HandleFunc("/.well-known/m-stash.json", corsMiddleware(handleServiceManifest))
	mux.HandleFunc("/v1/public/stashes", corsMiddleware(handlePublicDiscovery))
	mux.HandleFunc("/v1/public/stashes/", corsMiddleware(handlePublicDiscovery))
	mux.HandleFunc("/v1/public/profiles/", corsMiddleware(handlePublicProfile))
	mux.HandleFunc("/v1/db/", corsMiddleware(authenticate(handleDatabaseProxy)))
	mux.HandleFunc("/v1/ws/", authenticate(handleDatabaseWebSocket))
	mux.HandleFunc("/metrics", handleMetrics)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time": time.Now().String()})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := mongoClient.Ping(ctx, nil); err != nil {
			writeError(w, http.StatusServiceUnavailable, "Database is unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	server := &http.Server{
		Addr:              ":" + config.Port,
		Handler:           requestIDMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("🚀 Mongo Auth Gateway listening on http://localhost:%s", config.Port)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown failed: %v", err)
		}
		if err := mongoClient.Disconnect(ctx); err != nil {
			log.Printf("MongoDB disconnect failed: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("❌ Server crashed: %v", err)
	}
}

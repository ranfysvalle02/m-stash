package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
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

type Config struct {
	Port           string                    `json:"port"`
	MongoURI       string                    `json:"mongo_uri"`
	Database       string                    `json:"database"`
	JWTSecret      string                    `json:"jwt_secret"`
	AllowedOrigins []string                  `json:"allowed_origins"`
	Rules          map[string]CollectionRule `json:"rules"`
}

var (
	config Config
	db     *mongo.Database
)

const maxPayloadBytes = 1024 * 1024 // 1MB Request Payload Limit

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

// ============================================================================
// CONFIGURATION LOADER
// ============================================================================

func loadConfig() {
	config = Config{
		Port:           getEnv("PORT", "4000"),
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		Database:       getEnv("MONGO_DB", "app_db"),
		JWTSecret:      getEnv("JWT_SECRET", "super-secret-gateway-key-change-me"),
		AllowedOrigins: []string{"*"},
		Rules: map[string]CollectionRule{
			"portfolios": {
				Read:                  map[string]any{"$or": []any{map[string]any{"ownerId": "$auth.uid"}, map[string]any{"isPublic": true}}},
				Write:                 map[string]any{"ownerId": "$auth.uid"},
				RestrictedWriteFields: []string{"role", "isVerified"},
			},
			"profiles": {
				Read:                  true,
				Write:                 map[string]any{"_id": "$auth.uid"},
				RestrictedWriteFields: []string{"role", "permissions"},
			},
		},
	}

	configFile := "gateway.json"
	if _, err := os.Stat(configFile); err == nil {
		data, err := os.ReadFile(configFile)
		if err == nil {
			var fileConfig Config
			if err := json.Unmarshal(data, &fileConfig); err == nil {
				if fileConfig.Port != "" { config.Port = fileConfig.Port }
				if fileConfig.MongoURI != "" { config.MongoURI = fileConfig.MongoURI }
				if fileConfig.Database != "" { config.Database = fileConfig.Database }
				if fileConfig.JWTSecret != "" { config.JWTSecret = fileConfig.JWTSecret }
				if len(fileConfig.AllowedOrigins) > 0 { config.AllowedOrigins = fileConfig.AllowedOrigins }
				if len(fileConfig.Rules) > 0 { config.Rules = fileConfig.Rules }
				log.Println("Loaded configuration from gateway.json")
			}
		}
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
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
	if isIDField(keyContext) && len(v) == 24 && bson.HasHexObjectID(v) {
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

func authenticate(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing or invalid Authorization header"})
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			return []byte(config.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid or expired token"})
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

func handleSignUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Request body exceeds maximum limit (1MB)"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email and password required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	usersCol := db.Collection("_users")
	count, err := usersCol.CountDocuments(ctx, bson.M{"email": req.Email})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database query failed during user check"})
		return
	}
	if count > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "User already exists"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Error hashing password"})
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create user"})
		return
	}

	token, err := generateJWT(newUser.ID.Hex(), newUser.Email, newUser.Role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "user": newUser})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Request body exceeds maximum limit (1MB)"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var user User
	err := db.Collection("_users").FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid email or password"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid email or password"})
		return
	}

	token, err := generateJWT(user.ID.Hex(), user.Email, user.Role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func generateJWT(uid, email, role string) (string, error) {
	claims := Claims{
		UID:   uid,
		Email: email,
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.JWTSecret))
}

func handleDatabaseProxy(w http.ResponseWriter, r *http.Request, claims *Claims) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid endpoint path. Use /v1/db/{collection}/{action}"})
		return
	}
	collectionName, action := parts[2], parts[3]

	rule, exists := config.Rules[collectionName]
	if !exists {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Access to collection '%s' is restricted", collectionName)})
		return
	}

	var body struct {
		Query   map[string]any `json:"query"`
		Payload map[string]any `json:"payload"`
	}

	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Request payload exceeds maximum allowed size (1MB)"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Malformed JSON request body: " + err.Error()})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	coll := db.Collection(collectionName)

	switch action {
	case "find":
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		cursor, err := coll.Find(ctx, filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		var results []bson.M
		if err := cursor.All(ctx, &results); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Error decoding cursor results: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": results})

	case "findOne":
		filter, err := applySecurityFilter(body.Query, rule.Read, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		var result bson.M
		err = coll.FindOne(ctx, filter).Decode(&result)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Document not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})

	case "insertOne":
		validatedPayload, err := authorizeAndHydrateInsert(body.Payload, rule, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		res, err := coll.InsertOne(ctx, validatedPayload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": res})

	case "updateOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		sanitizedPayload, err := sanitizeUpdatePayload(body.Payload, rule, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		res, err := coll.UpdateOne(ctx, filter, bson.M{"$set": sanitizedPayload})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": res})

	case "deleteOne":
		filter, err := applySecurityFilter(body.Query, rule.Write, claims)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		res, err := coll.DeleteOne(ctx, filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": res})

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Unsupported action '%s'", action)})
	}
}

// ============================================================================
// MAIN ENTRYPOINT
// ============================================================================

func main() {
	loadConfig()

	log.Println("🔌 Connecting to MongoDB...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(config.MongoURI))
	if err != nil {
		log.Fatalf("❌ Failed to connect to Mongo: %v", err)
	}
	db = client.Database(config.Database)
	log.Printf("✅ Connected to Mongo database: '%s'", config.Database)

	http.HandleFunc("/v1/auth/signup", corsMiddleware(handleSignUp))
	http.HandleFunc("/v1/auth/login", corsMiddleware(handleLogin))
	http.HandleFunc("/v1/db/", corsMiddleware(authenticate(handleDatabaseProxy)))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time": time.Now().String()})
	})

	log.Printf("🚀 Mongo Auth Gateway listening on http://localhost:%s", config.Port)
	if err := http.ListenAndServe(":"+config.Port, nil); err != nil {
		log.Fatalf("❌ Server crashed: %v", err)
	}
}

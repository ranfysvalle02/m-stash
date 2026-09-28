package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/bcrypt"
)

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

func (app *application) authenticate(next func(w http.ResponseWriter, r *http.Request, claims *Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
			return
		}

		tokenString := strings.TrimPrefix(authorization, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return []byte(app.config.JWTSecret), nil
		},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithIssuer(app.config.JWTIssuer),
			jwt.WithAudience(app.config.JWTAudience),
			jwt.WithLeeway(30*time.Second),
		)

		if err != nil || !token.Valid || claims.UID == "" || claims.Subject != claims.UID {
			writeError(w, http.StatusForbidden, "Invalid or expired token")
			return
		}
		next(w, r, claims)
	}
}

func (app *application) handleSignUp(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &request, false) {
		return
	}
	request.Email = normalizeEmail(request.Email)
	if err := validateSignUpCredentials(request.Email, request.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	users := app.database.Collection("_users")
	count, err := users.CountDocuments(ctx, bson.M{"email": request.Email})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database query failed during user check")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "User already exists")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error hashing password")
		return
	}
	user := User{
		ID:           bson.NewObjectID(),
		Email:        request.Email,
		PasswordHash: string(passwordHash),
		Role:         "user",
		CreatedAt:    time.Now(),
	}
	if _, err := users.InsertOne(ctx, user); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}

	token, err := app.generateJWT(user.ID.Hex(), user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "user": user})
}

func (app *application) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &request, false) {
		return
	}
	request.Email = normalizeEmail(request.Email)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var user User
	if err := app.database.Collection("_users").FindOne(ctx, bson.M{"email": request.Email}).Decode(&user); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	token, err := app.generateJWT(user.ID.Hex(), user.Email, user.Role)
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

func (app *application) generateJWT(uid, email, role string) (string, error) {
	claims := Claims{
		UID:   uid,
		Email: email,
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    app.config.JWTIssuer,
			Subject:   uid,
			Audience:  jwt.ClaimStrings{app.config.JWTAudience},
			ID:        bson.NewObjectID().Hex(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(app.config.JWTSecret))
}

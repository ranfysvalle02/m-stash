package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

const maxPayloadBytes = 1024 * 1024

func (app *application) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data: https:; object-src 'none'; script-src 'self'; style-src 'self'")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if app.requestUsesHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (app *application) requestUsesHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !app.config.TrustProxy {
		return false
	}
	forwardedProtocol, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(forwardedProtocol), "https")
}

func (app *application) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false

		if origin != "" {
			for _, allowedOrigin := range app.config.AllowedOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowed = true
					w.Header().Set("Access-Control-Allow-Origin", origin)
					if allowedOrigin != "*" {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					break
				}
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")

		if r.Method == http.MethodOptions {
			if !allowed && origin != "" && len(app.config.AllowedOrigins) > 0 && app.config.AllowedOrigins[0] != "*" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func (app *application) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowedOrigin := range app.config.AllowedOrigins {
		if allowedOrigin == "*" || allowedOrigin == origin {
			return true
		}
	}
	return false
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

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return false
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any, allowEmptyBody bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)
	err := json.NewDecoder(r.Body).Decode(destination)
	if err == nil {
		return true
	}
	if allowEmptyBody && errors.Is(err, io.EOF) {
		return true
	}

	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, "Request body exceeds maximum limit (1MB)")
		return false
	}
	writeError(w, http.StatusBadRequest, "Malformed JSON request body: "+err.Error())
	return false
}

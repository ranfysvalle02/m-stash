package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationHandlerServesClientRoute(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/app/posts", nil)
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("client route status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("client route content type = %q, want HTML", contentType)
	}
	if !strings.Contains(response.Body.String(), "<div id=\"root\">") {
		t.Fatal("client route did not serve the frontend entry point")
	}
}

func TestApplicationHandlerKeepsUnknownAPIRoutesJSON(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)
	response := httptest.NewRecorder()

	app.Handler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown API route status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("unknown API route content type = %q, want application/json", contentType)
	}
	if !strings.Contains(response.Body.String(), "API route not found") {
		t.Fatalf("unknown API route body = %s", response.Body.String())
	}
}

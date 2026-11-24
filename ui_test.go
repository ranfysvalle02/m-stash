package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationHandlerServesClientRoute(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/app/resources/project", nil)
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

func TestFrontendRedirectsSecondaryHostsToPublicBaseURL(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}, PublicBaseURL: "https://m-stash.com"}, nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/public/config/release?ref=share", nil)
	request.Host = "m-stash.onrender.com"
	response := httptest.NewRecorder()
	app.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMovedPermanently)
	}
	if location := response.Header().Get("Location"); location != "https://m-stash.com/public/config/release?ref=share" {
		t.Fatalf("Location = %q", location)
	}

	for _, host := range []string{"m-stash.com", "localhost:4000", "127.0.0.1:4000"} {
		request := httptest.NewRequest(http.MethodGet, "/discover", nil)
		request.Host = host
		response := httptest.NewRecorder()
		app.Handler(nil).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("host %s status = %d, want %d", host, response.Code, http.StatusOK)
		}
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)
	apiRequest.Host = "m-stash.onrender.com"
	apiResponse := httptest.NewRecorder()
	app.Handler(nil).ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("API route status = %d, want it served without redirect", apiResponse.Code)
	}
}

func TestFrontendRendersRouteSpecificSEOTags(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}, PublicBaseURL: "https://m-stash.com"}, nil, nil)
	tests := []struct {
		path    string
		want    []string
		notWant []string
	}{
		{
			path: "/",
			want: []string{`<link rel="canonical" href="https://m-stash.com/discover" />`, `application/ld+json`},
		},
		{
			path: "/guide",
			want: []string{
				"<title>Build guide: ship the app, skip the backend setup — m-stash</title>",
				`<meta property="og:url" content="https://m-stash.com/guide" />`,
				`<meta property="og:image" content="https://m-stash.com/og.png" />`,
			},
			notWant: []string{"noindex", "application/ld+json"},
		},
		{
			path: "/Alex/Save/Slot-One",
			want: []string{`<link rel="canonical" href="https://m-stash.com/alex/save/slot-one" />`, "slot-one · save by @alex"},
		},
		{
			path:    "/vibe/AbCd-fGh_jKl",
			want:    []string{"backend sketched with m-stash", `content="https://m-stash.com/vibe/AbCd-fGh_jKl"`, "noindex"},
			notWant: []string{"Page not found"},
		},
		{
			path:    "/app/resources/project",
			want:    []string{`<meta name="robots" content="noindex, nofollow" />`},
			notWant: []string{`rel="canonical"`},
		},
		{
			path: "/a/b/c/d",
			want: []string{"Page not found", "noindex"},
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = "m-stash.com"
			response := httptest.NewRecorder()
			app.Handler(nil).ServeHTTP(response, request)
			body := response.Body.String()
			if strings.Count(body, "<title>") != 1 {
				t.Fatalf("expected exactly one <title>, body:\n%s", body)
			}
			for _, want := range test.want {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(body, notWant) {
					t.Errorf("body unexpectedly contains %q", notWant)
				}
			}
		})
	}
}

func TestRobotsAndSitemapUseRequestOriginWithoutPublicBaseURL(t *testing.T) {
	app := newApplication(Config{AllowedOrigins: []string{"*"}}, nil, nil)
	for path, want := range map[string]string{
		"/robots.txt":  "Sitemap: http://self-hosted.example/sitemap.xml",
		"/sitemap.xml": "<loc>http://self-hosted.example/discover</loc>",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "self-hosted.example"
		response := httptest.NewRecorder()
		app.Handler(nil).ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s status = %d body = %s, want %q", path, response.Code, response.Body.String(), want)
		}
	}
}

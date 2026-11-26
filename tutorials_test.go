package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestTutorialsParseAndStampTheDeploymentOrigin(t *testing.T) {
	tutorials, err := loadTutorials()
	if err != nil {
		t.Fatal(err)
	}
	if len(tutorials) < 5 || tutorials[0].Slug != "first-app-in-5-minutes" {
		t.Fatalf("tutorials = %+v", tutorials)
	}
	app := &application{config: Config{PublicBaseURL: "https://stash.example"}}
	for _, entry := range tutorials {
		rec := httptest.NewRecorder()
		app.handleTutorials(rec, httptest.NewRequest(http.MethodGet, "/v1/tutorials/"+entry.Slug+".md", nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || strings.Contains(body, "{{API}}") {
			t.Fatalf("%s: status %d, unstamped placeholder left: %v", entry.Slug, rec.Code, strings.Contains(body, "{{API}}"))
		}
		if strings.Contains(body, "sk_live") || strings.Contains(body, "sk_test") {
			t.Fatalf("%s: tutorials must never contain a real-looking secret key", entry.Slug)
		}
	}

	rec := httptest.NewRecorder()
	app.handleTutorials(rec, httptest.NewRequest(http.MethodGet, "/v1/tutorials/login-and-private-data", nil))
	var payload struct {
		Data struct {
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload.Data.Title == "" || !strings.Contains(payload.Data.Markdown, `import { createClient } from "https://stash.example/mstash.js"`) {
		t.Fatalf("tutorial JSON must carry the stamped Markdown: %s", rec.Body.String())
	}

	for _, missing := range []string{"/v1/tutorials/nope", "/v1/tutorials/login-and-private-data/extra"} {
		rec := httptest.NewRecorder()
		app.handleTutorials(rec, httptest.NewRequest(http.MethodGet, missing, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d", missing, rec.Code)
		}
	}
}

func TestParseTutorialRejectsMissingFrontmatter(t *testing.T) {
	if _, err := parseTutorial("ok-slug", "# no frontmatter"); err == nil {
		t.Fatal("missing frontmatter must fail")
	}
	if _, err := parseTutorial("ok-slug", "---\ntitle: X\n---\nbody"); err == nil {
		t.Fatal("frontmatter without summary/minutes must fail")
	}
}

func TestTutorialPagesGetTheirOwnMeta(t *testing.T) {
	app := &application{}
	meta := app.pageMetaFor(context.Background(), "/tutorials/backend-with-your-secret-key")
	if !strings.HasPrefix(meta.Title, "Run a backend with your secret key") || meta.NoIndex || meta.Description == "" {
		t.Fatalf("meta = %+v", meta)
	}
	if missing := app.pageMetaFor(context.Background(), "/tutorials/nope"); !missing.NoIndex {
		t.Fatalf("unknown tutorial must be noindex: %+v", missing)
	}
}

// The client is imported cross-origin as an ES module, which browsers only
// allow with a CORS header.
func TestClientScriptIsImportableFromAnyOrigin(t *testing.T) {
	rec := httptest.NewRecorder()
	(&application{}).handleClientScript(rec, httptest.NewRequest(http.MethodGet, "/mstash.js", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(rec.Body.String(), "export { MStashError, createClient }") {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-TypeScript-Types") != "/mstash.d.ts" {
		t.Fatalf("X-TypeScript-Types = %q", rec.Header().Get("X-TypeScript-Types"))
	}

	rec = httptest.NewRecorder()
	(&application{}).handleClientScript(rec, httptest.NewRequest(http.MethodGet, "/mstash-embed.js", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") ||
		rec.Header().Get("X-TypeScript-Types") != "" ||
		strings.Contains(rec.Body.String(), "export { MStashError, createClient }") ||
		!strings.Contains(rec.Body.String(), "global.mStash = instance") {
		t.Fatalf("embed: status=%d headers=%v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	(&application{}).handleClientScript(rec, httptest.NewRequest(http.MethodGet, "/mstash.d.ts", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/typescript") ||
		!strings.Contains(rec.Body.String(), "export declare function createClient") {
		t.Fatalf("types: status=%d headers=%v", rec.Code, rec.Header())
	}
}

// A server holding the secret key must be able to bump a shared counter
// atomically; reads stay refused.
func TestSecretKeyMayIncrementButNotReadShared(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("sk_test_secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{}
	record := &AppRecord{SecretKeyHash: string(hash)}
	for method, want := range map[string]int{http.MethodPatch: http.StatusTeapot, http.MethodGet: http.StatusForbidden} {
		request := httptest.NewRequest(method, "/v1/shared/resources/menus/today", nil).WithContext(withApp(context.Background(), record))
		request.Header.Set("Authorization", "Bearer sk_test_secret")
		rec := httptest.NewRecorder()
		app.authenticateSharedResource(func(w http.ResponseWriter, _ *http.Request, claims *Claims) {
			if claims.Role != sharedServiceRole {
				t.Fatalf("role = %q", claims.Role)
			}
			w.WriteHeader(http.StatusTeapot)
		})(rec, request)
		if rec.Code != want {
			t.Fatalf("%s: status %d, want %d", method, rec.Code, want)
		}
	}
}

func TestPublicStatsCanBeTurnedOff(t *testing.T) {
	app := &application{config: Config{PublicStats: false}}
	rec := httptest.NewRecorder()
	app.handlePublicStats(rec, httptest.NewRequest(http.MethodGet, "/v1/stats", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPublicStatsCacheBacksOffAndNeverQueues(t *testing.T) {
	var cache publicStatsCache
	calls := 0
	failing := func(context.Context) (publicStats, error) { calls++; return publicStats{}, errors.New("db down") }
	if _, err := cache.get(context.Background(), failing); err == nil {
		t.Fatal("first failure with nothing cached must surface an error")
	}
	// Within the TTL a failed refresh is not retried, so a down database is hit once.
	for range 5 {
		if _, err := cache.get(context.Background(), failing); !errors.Is(err, errPublicStatsWarming) {
			t.Fatalf("err = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("compute ran %d times, want 1", calls)
	}

	// Once loaded, a refresh in flight serves the last good numbers immediately.
	cache.attemptedAt = time.Time{}
	stats, _ := cache.get(context.Background(), func(context.Context) (publicStats, error) { return publicStats{Stashers: 7}, nil })
	if stats.Stashers != 7 {
		t.Fatalf("stats = %+v", stats)
	}
	cache.attemptedAt = time.Time{}
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _ = cache.get(context.Background(), func(context.Context) (publicStats, error) { <-release; return publicStats{Stashers: 8}, nil })
		close(done)
	}()
	for {
		cache.mu.Lock()
		refreshing := cache.refreshing
		cache.mu.Unlock()
		if refreshing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if stats, err := cache.get(context.Background(), failing); err != nil || stats.Stashers != 7 {
		t.Fatalf("a caller during a refresh must get the last good stats at once: %+v %v", stats, err)
	}
	close(release)
	<-done
}

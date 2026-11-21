package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testVibeShareRecord() *vibeShareRecord {
	return &vibeShareRecord{
		ID:   "abcdefghijkl",
		Idea: `a notes app </script><script>alert(1)</script>`,
		Plan: vibePlan{
			Summary: "Notes",
			DataModel: []vibeModelPart{
				{Name: "Note", Scope: "personal", Type: "note", Visibility: "private"},
				{Name: "Bad", Scope: "personal", Type: "Not A Slug!"},
				{Name: "Note again", Scope: "personal", Type: "note", Visibility: "private"},
			},
			Snippets: []vibeSnippet{{Title: "Save", Language: "javascript", Code: "fetch('/v1/me/resources/note')"}},
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
	}
}

func TestVibeShareIDRoundTrip(t *testing.T) {
	id, err := newVibeShareID()
	if err != nil {
		t.Fatal(err)
	}
	if !validVibeShareID(id) {
		t.Fatalf("generated id %q is not valid", id)
	}
	for _, bad := range []string{"", "short", "abcdefghijk!", "abcdefghijklm", "../../etc/pa"} {
		if validVibeShareID(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func TestRenderVibeStarterIndexEscapesIdea(t *testing.T) {
	page, err := renderVibeStarterIndex(testVibeShareRecord(), vibeStarterConfig{App: "pk_test"}, "/v1/vibe/abcdefghijkl/")
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatalf("idea was not escaped:\n%s", body)
	}
	if !strings.Contains(body, `src="/v1/vibe/abcdefghijkl/app.js"`) {
		t.Fatalf("asset base missing:\n%s", body)
	}
	if strings.Count(body, `"type":"note"`) != 1 || strings.Contains(body, "Not A Slug") {
		t.Fatalf("starter parts not filtered:\n%s", body)
	}
}

func TestBuildVibeStarterZip(t *testing.T) {
	archive, err := buildVibeStarterZip(testVibeShareRecord(), "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, _ := io.ReadAll(handle)
		handle.Close()
		files[file.Name] = string(contents)
	}
	for _, name := range []string{"README.md", "index.html", "app.js", "app.css", "plan.json"} {
		if _, ok := files["m-stash-starter/"+name]; !ok {
			t.Fatalf("zip missing %s; got %v", name, files)
		}
	}
	index := files["m-stash-starter/index.html"]
	if !strings.Contains(index, `"api":"https://example.test"`) || !strings.Contains(index, vibeStarterPlaceholder) {
		t.Fatalf("zip index not configured:\n%s", index)
	}
	if !strings.Contains(index, `src="app.js"`) {
		t.Fatalf("zip index should use relative assets:\n%s", index)
	}
	if !strings.Contains(files["m-stash-starter/README.md"], "https://example.test/console") {
		t.Fatal("README missing console link")
	}
}

func TestNormalizeVibePlanFields(t *testing.T) {
	if got := normalizeVibeType("  Recipe Book!! "); got != "recipe-book" {
		t.Fatalf("type = %q", got)
	}
	if got := normalizeVibeVisibility("personal", "Authenticated"); got != "private" {
		t.Fatalf("personal authenticated = %q", got)
	}
	if got := normalizeVibeVisibility("shared", "authenticated"); got != "authenticated" {
		t.Fatalf("shared authenticated = %q", got)
	}
	if got := normalizeVibeScope("SHARED"); got != "shared" {
		t.Fatalf("scope = %q", got)
	}
}

func TestPublicBaseURLHonorsForwardedProto(t *testing.T) {
	app := &application{}
	request := httptest.NewRequest(http.MethodGet, "http://m-stash.example/v1/vibe", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := app.publicBaseURL(request); got != "https://m-stash.example" {
		t.Fatalf("base = %q", got)
	}
}

func TestVibeShareRejectsBadIDWithoutDatabase(t *testing.T) {
	app := &application{}
	response := httptest.NewRecorder()
	app.handleVibeShare(response, httptest.NewRequest(http.MethodGet, "/v1/vibe/nope/preview", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

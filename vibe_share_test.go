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
		Idea: "a site for my guitar and vinyl shop </script><script>alert(1)</script>",
		Spec: vibeSpec{
			Name:    "Riff & Groove </script><script>alert(1)</script>",
			Tagline: "Guitars, vinyl, and in-store gigs",
			Accent:  "amber",
			Icon:    "cart",
			Sections: []vibeSection{
				{
					Type: "catalog", Title: "Catalog", Audience: vibeAudienceOwner, View: "grid",
					Fields: []vibeField{
						{Key: "name", Label: "Name", Kind: "text"},
						{Key: "price", Label: "Price", Kind: "price"},
					},
					Samples: []map[string]any{
						{"name": "1965 Fender Stratocaster", "price": 2400.0},
					},
				},
				{
					Type: "wishlist", Title: "My wishlist", Audience: vibeAudienceMember, View: "list",
					Fields: []vibeField{{Key: "item", Label: "Item", Kind: "text"}},
				},
			},
		},
		PublishableKey: "",
		Version:        vibeShareVersion,
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:      time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
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

func TestRenderVibeStarterIndexEscapesAndStripsSamples(t *testing.T) {
	page, err := renderVibeStarterIndex(testVibeShareRecord(), vibeStarterConfig{App: "pk_test", Mode: "preview"}, "/v1/vibe/abcdefghijkl/")
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatalf("spec name was not escaped:\n%s", body)
	}
	if !strings.Contains(body, `src="/v1/vibe/abcdefghijkl/app.js"`) {
		t.Fatalf("asset base missing from app.js:\n%s", body)
	}
	if !strings.Contains(body, `src="/v1/vibe/abcdefghijkl/views.js"`) {
		t.Fatalf("asset base missing from views.js:\n%s", body)
	}
	if !strings.Contains(body, `href="/v1/vibe/abcdefghijkl/app.css"`) {
		t.Fatalf("asset base missing from app.css:\n%s", body)
	}
	// Seeded samples must never reach the client; it reads live records instead.
	if strings.Contains(body, "Stratocaster") || strings.Contains(body, `"samples"`) {
		t.Fatalf("samples were not stripped from the client config:\n%s", body)
	}
	if !strings.Contains(body, `"mode":"preview"`) {
		t.Fatalf("mode missing from config:\n%s", body)
	}
}

func TestBuildVibeStarterZip(t *testing.T) {
	archive, err := buildVibeStarterZip(testVibeShareRecord(), "https://example.test", false)
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
	for _, name := range []string{"README.md", "AGENTS.md", "SKILL.md", "index.html", "app.js", "app.css", "views.js", "mstash.json"} {
		if _, ok := files["m-stash-starter/"+name]; !ok {
			t.Fatalf("zip missing %s; got %v", name, keys(files))
		}
	}
	// The flat SKILL.md is the spec-tailored agent skill; it must sit at the
	// project root (not inside a .cursor/ folder) per the plug-and-play layout.
	if _, nested := files["m-stash-starter/.cursor/skills/m-stash/SKILL.md"]; nested {
		t.Fatal("starter SKILL.md must stay flat at the root, not nested under .cursor/")
	}
	index := files["m-stash-starter/index.html"]
	if !strings.Contains(index, `"api":"https://example.test"`) {
		t.Fatalf("zip index not pointed at the live base URL:\n%s", index)
	}
	if !strings.Contains(index, vibeStarterPlaceholder) {
		t.Fatalf("zip index without a live key should use the placeholder:\n%s", index)
	}
	if !strings.Contains(index, `src="app.js"`) || !strings.Contains(index, `src="views.js"`) {
		t.Fatalf("zip index should use relative assets:\n%s", index)
	}
	if !strings.Contains(files["m-stash-starter/README.md"], "https://example.test/console") {
		t.Fatal("README missing console link")
	}
	if !strings.Contains(files["m-stash-starter/AGENTS.md"], "https://example.test/llms.txt") {
		t.Fatal("AGENTS.md missing llms.txt reference")
	}
	// The owner password is never known to the share, so it can never land in the zip.
	if strings.Contains(strings.ToLower(files["m-stash-starter/mstash.json"]), "password") {
		t.Fatal("mstash.json must not contain any password field")
	}
}

// TestVibeRecipeStartersStaySampleFree renders each recipe through the starter
// and confirms the seeded owner samples (which exist only so the launched app
// looks alive) never reach the embedded client config — it reads live records
// over the API instead.
func TestVibeRecipeStartersStaySampleFree(t *testing.T) {
	// One unique token per recipe, drawn from its seeded owner samples.
	leaks := map[string]string{
		"leaderboard":          "ace_of_space",
		"user-tickets":         "nominal",
		"appointments":         "Okonkwo",
		"barber-bookings":      "Skin Fade",
		"real-estate-bookings": "Harbor View",
		"storefront":           "Stratocaster",
	}
	for _, recipe := range vibeRecipes {
		t.Run(recipe.ID, func(t *testing.T) {
			record := &vibeShareRecord{
				ID:        "abcdefghijkl",
				Idea:      recipe.Name,
				Spec:      finalizeRecipe(recipe.build()),
				Version:   vibeShareVersion,
				CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ExpiresAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
			}
			page, err := renderVibeStarterIndex(record, vibeStarterConfig{App: "pk_test", Mode: "preview"}, "/v1/vibe/abcdefghijkl/")
			if err != nil {
				t.Fatal(err)
			}
			body := string(page)
			if strings.Contains(body, `"samples"`) {
				t.Fatalf("%s: samples key leaked into client config:\n%s", recipe.ID, body)
			}
			if token := leaks[recipe.ID]; token != "" && strings.Contains(body, token) {
				t.Fatalf("%s: seeded sample %q leaked into client config", recipe.ID, token)
			}
		})
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPublicBaseURLHonorsForwardedProto(t *testing.T) {
	app := &application{config: Config{TrustProxy: true}}
	request := httptest.NewRequest(http.MethodGet, "http://m-stash.example/v1/vibe", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := app.publicBaseURL(request); got != "https://m-stash.example" {
		t.Fatalf("base = %q", got)
	}
}

func TestPublicBaseURLPrefersConfiguredOrigin(t *testing.T) {
	app := &application{config: Config{PublicBaseURL: "https://canonical.example/"}}
	request := httptest.NewRequest(http.MethodGet, "http://m-stash.internal/v1/vibe", nil)
	if got := app.publicBaseURL(request); got != "https://canonical.example" {
		t.Fatalf("base = %q", got)
	}
}

func TestVibeShareTTLCapsAtFiveDays(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		0:                   72 * time.Hour,
		24 * time.Hour:      24 * time.Hour,
		5 * 24 * time.Hour:  5 * 24 * time.Hour,
		30 * 24 * time.Hour: vibeShareMaxTTL,
	}
	for configured, want := range cases {
		app := &application{config: Config{VibeShareTTL: configured}}
		if got := app.vibeShareTTL(); got != want {
			t.Fatalf("vibeShareTTL(%s) = %s, want %s", configured, got, want)
		}
	}
	if vibeShareMaxTTL != 5*24*time.Hour {
		t.Fatalf("max ttl = %s, want 120h", vibeShareMaxTTL)
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

func TestVibePreviewCSPNamesOwnHostForWebKit(t *testing.T) {
	csp := vibePreviewCSP("https://m-stash.example")
	for _, directive := range []string{"script-src 'self' https://m-stash.example", "style-src 'self' https://m-stash.example", "connect-src 'self' https://m-stash.example"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP %q missing %q", csp, directive)
		}
	}
	if !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "unsafe-") {
		t.Fatalf("CSP weakened: %q", csp)
	}
	if got := vibePreviewCSP("http://localhost:4311"); !strings.Contains(got, "script-src 'self' http://localhost:4311;") {
		t.Fatalf("port host not preserved: %q", got)
	}
}

func TestVibePreviewCSPRejectsInjectedHosts(t *testing.T) {
	hostile := []string{
		"https://evil.example; script-src *",
		"https://evil.example script-src",
		"https://a.example,b.example",
		"https://evil.example'",
		"https://",
		"javascript://evil.example",
		"",
	}
	for _, base := range hostile {
		csp := vibePreviewCSP(base)
		if strings.Contains(csp, "evil") || strings.Count(csp, "script-src") != 1 || strings.Contains(csp, "script-src *") {
			t.Fatalf("hostile base %q leaked into CSP: %q", base, csp)
		}
		if !strings.Contains(csp, "script-src 'self';") {
			t.Fatalf("hostile base %q should fall back to 'self' only: %q", base, csp)
		}
	}
}

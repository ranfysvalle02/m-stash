package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// skillNameFormat mirrors the Cursor/Claude agent-skill rule: a skill name is
// lowercase letters, digits, and single hyphens.
var skillNameFormat = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// skillFrontmatter extracts the name and description from a SKILL.md's leading
// YAML frontmatter, failing the test if the fences are missing.
func skillFrontmatter(t *testing.T, body string) (name, description string) {
	t.Helper()
	if !strings.HasPrefix(body, "---\n") {
		t.Fatalf("SKILL.md missing opening frontmatter fence:\n%s", body)
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("SKILL.md missing closing frontmatter fence:\n%s", body)
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		switch {
		case strings.HasPrefix(line, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return name, description
}

// assertValidSkillName enforces the name constraints an agent host requires.
func assertValidSkillName(t *testing.T, name string) {
	t.Helper()
	if name == "" {
		t.Fatal("skill name is empty")
	}
	if len(name) > 64 {
		t.Fatalf("skill name %q exceeds 64 chars", name)
	}
	if !skillNameFormat.MatchString(name) {
		t.Fatalf("skill name %q is not lowercase/hyphen slug", name)
	}
}

// sectionTypeKey is the sorted set of a spec's section types; two specs built
// from the same recipe share it, so it identifies which recipe a blueprint
// routed to.
func sectionTypeKey(spec vibeSpec) string {
	types := make([]string, 0, len(spec.Sections))
	for _, section := range spec.Sections {
		types = append(types, section.Type)
	}
	sort.Strings(types)
	return strings.Join(types, ",")
}

// TestVibeRecipeRemixIdeasRouteBackToTheirRecipe is the keyword-collision guard:
// each recipe's RemixIdea must survive idea validation and, when run through the
// no-model-key blueprint path (exactly as the backend does after validation),
// map back to its OWN recipe rather than an earlier one in the roster.
func TestVibeRecipeRemixIdeasRouteBackToTheirRecipe(t *testing.T) {
	for _, recipe := range vibeRecipes {
		t.Run(recipe.ID, func(t *testing.T) {
			if strings.TrimSpace(recipe.RemixIdea) == "" {
				t.Fatalf("%s: RemixIdea is empty", recipe.ID)
			}
			cleaned, err := validateVibeIdea(recipe.RemixIdea)
			if err != nil {
				t.Fatalf("%s: RemixIdea rejected by validation: %v", recipe.ID, err)
			}
			want := sectionTypeKey(finalizeRecipe(recipe.build()))
			got := sectionTypeKey(buildVibeBlueprintSpec(cleaned))
			if got != want {
				t.Fatalf("%s: RemixIdea %q routed to sections [%s], want this recipe's sections [%s]",
					recipe.ID, cleaned, got, want)
			}
		})
	}
}

// TestMetricRouteBucketsVibeAndSkill proves the operator telemetry now counts
// the recipe list and the skill endpoint separately from the per-share
// /v1/vibe/{id} bucket, so they stop being lumped together.
func TestMetricRouteBucketsVibeAndSkill(t *testing.T) {
	cases := map[string]string{
		"/skill.md":                      "/skill.md",
		"/v1/vibe":                       "/v1/vibe",
		"/v1/vibe/recipes":               "/v1/vibe/recipes",
		"/v1/vibe/abcdefghijkl":          "/v1/vibe/{id}",
		"/v1/vibe/abcdefghijkl/zip":      "/v1/vibe/{id}",
		"/v1/vibe/abcdefghijkl/skill.md": "/v1/vibe/{id}",
		"/v1/vibe/abcdefghijkl/preview":  "/v1/vibe/{id}",
	}
	for path, want := range cases {
		if got := metricRoute(path); got != want {
			t.Fatalf("metricRoute(%q) = %q, want %q", path, got, want)
		}
	}
	if metricRoute("/v1/vibe/recipes") == metricRoute("/v1/vibe/abcdefghijkl") {
		t.Fatal("/v1/vibe/recipes must bucket separately from /v1/vibe/{id}")
	}
}

// TestGenericVibeSkillHasValidFrontmatterAndStampsOrigin checks the deployment-
// wide skill: valid frontmatter, this origin and llms.txt stamped in, the trigger
// vocabulary present, and the sandbox block gated on TRY_SANDBOX.
func TestGenericVibeSkillHasValidFrontmatterAndStampsOrigin(t *testing.T) {
	skill := genericVibeSkill("https://live.example", true)
	name, description := skillFrontmatter(t, skill)
	if name != "m-stash" {
		t.Fatalf("generic skill name = %q, want m-stash", name)
	}
	assertValidSkillName(t, name)
	if description == "" {
		t.Fatal("generic skill description is empty")
	}
	for _, want := range []string{"m-stash", "X-M-Stash-App", "pk_", "/v1/me/resources"} {
		if !strings.Contains(skill, want) {
			t.Fatalf("generic skill missing trigger/term %q:\n%s", want, skill)
		}
	}
	if !strings.Contains(skill, "https://live.example") {
		t.Fatalf("generic skill should stamp the deployment origin:\n%s", skill)
	}
	if !strings.Contains(skill, "https://live.example/llms.txt") {
		t.Fatalf("generic skill should reference llms.txt:\n%s", skill)
	}
	if !strings.Contains(skill, "/v1/try") {
		t.Fatalf("generic skill with TRY_SANDBOX should include the zero-setup sandbox call:\n%s", skill)
	}

	closed := genericVibeSkill("https://live.example", false)
	if strings.Contains(closed, "/v1/try") {
		t.Fatalf("generic skill without TRY_SANDBOX must not advertise the sandbox:\n%s", closed)
	}
}

// TestVibeAppSkillEmbedsKeyAndStaysSampleFree is the per-app skill's core
// guarantee: valid frontmatter named after the app, the live key and the app's
// own fields/options embedded, and NO seeded samples, passwords, or claim tokens.
func TestVibeAppSkillEmbedsKeyAndStaysSampleFree(t *testing.T) {
	record := &vibeShareRecord{
		ID:             "abcdefghijkl",
		Idea:           "a record store",
		Spec:           finalizeRecipe(storefrontRecipe()),
		PublishableKey: "pk_live_store_123",
		Version:        vibeShareVersion,
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:      time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
	}
	skill := vibeAppSkill(record, "https://live.example")

	name, description := skillFrontmatter(t, skill)
	assertValidSkillName(t, name)
	if !strings.HasPrefix(name, "m-stash-") {
		t.Fatalf("app skill name %q should be prefixed with m-stash-", name)
	}
	if description == "" {
		t.Fatal("app skill description is empty")
	}

	if !strings.Contains(skill, "pk_live_store_123") {
		t.Fatalf("app skill must embed the live publishable key:\n%s", skill)
	}
	if strings.Contains(skill, vibeStarterPlaceholder) {
		t.Fatalf("app skill with a live key must not use the placeholder:\n%s", skill)
	}
	if !strings.Contains(skill, "https://live.example/llms.txt") {
		t.Fatalf("app skill should reference llms.txt:\n%s", skill)
	}

	// The storefront catalog's fields must be listed so an agent knows the shape.
	for _, field := range []string{"name", "price", "category"} {
		if !strings.Contains(skill, "`"+field+"`") {
			t.Fatalf("app skill missing catalog field %q:\n%s", field, skill)
		}
	}

	// Seeded owner samples power the live app but must never appear in the skill.
	for _, leak := range []string{"Stratocaster", "Pink Floyd", "Open Mic", "1965"} {
		if strings.Contains(skill, leak) {
			t.Fatalf("app skill leaked seeded sample %q:\n%s", leak, skill)
		}
	}
	// The skill must warn against shipping the secret key — and carry no
	// password or claim-token material of its own (the record stores neither).
	if !strings.Contains(skill, "sk_") {
		t.Fatalf("app skill should warn never to ship the secret key:\n%s", skill)
	}
	// The verify script's throwaway signup is the only credential-shaped text
	// allowed; it is a fixed placeholder, never a stored value.
	lower := strings.ReplaceAll(strings.ToLower(skill), `"password":"verify-password-123"`, "")
	for _, forbidden := range []string{"password", "claim", "@sandbox.invalid"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("app skill leaked forbidden token %q:\n%s", forbidden, skill)
		}
	}
	// The only key ever embedded is the publishable one; no secret value leaks.
	if strings.Contains(skill, "sk_live") || strings.Contains(skill, "sk_test") {
		t.Fatalf("app skill leaked a secret key value:\n%s", skill)
	}
}

// TestVibeAppSkillQuotesDescriptionAgainstInjection confirms a spec name with a
// colon (or quotes) can't break the YAML frontmatter.
func TestVibeAppSkillQuotesDescriptionAgainstInjection(t *testing.T) {
	record := &vibeShareRecord{
		ID: "abcdefghijkl",
		Spec: vibeSpec{
			Name: `Notes: "Pro" edition`,
			Sections: []vibeSection{
				{
					Type: "note", Title: "Notes", Audience: vibeAudienceMember, View: "list",
					Fields: []vibeField{{Key: "body", Label: "Body", Kind: "longtext"}},
				},
			},
		},
		PublishableKey: "pk_live_notes",
		Version:        vibeShareVersion,
	}
	skill := vibeAppSkill(record, "https://live.example")
	name, description := skillFrontmatter(t, skill)
	assertValidSkillName(t, name) // a colon/quote name must still yield a valid slug
	if !strings.HasPrefix(description, `"`) || !strings.HasSuffix(description, `"`) {
		t.Fatalf("description with punctuation must be a quoted scalar: %q", description)
	}
	if strings.Contains(description, `"Pro"`) {
		t.Fatalf("raw double quotes must be escaped in the description: %q", description)
	}
}

// TestHandleSkillServesGenericMarkdown exercises the always-on GET /skill.md
// endpoint: markdown content type, valid frontmatter, download disposition, and
// method enforcement.
func TestHandleSkillServesGenericMarkdown(t *testing.T) {
	app := newApplication(Config{TrySandbox: true}, nil, nil)

	rec := httptest.NewRecorder()
	app.handleSkill(rec, httptest.NewRequest(http.MethodGet, "http://m-stash.example/skill.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("content-type = %q, want text/markdown", ct)
	}
	body := rec.Body.String()
	name, description := skillFrontmatter(t, body)
	if name != "m-stash" || description == "" {
		t.Fatalf("served skill frontmatter name=%q description=%q", name, description)
	}
	if !strings.Contains(body, "http://m-stash.example") {
		t.Fatalf("served skill should stamp the request origin:\n%s", body)
	}
	if !strings.Contains(body, "/v1/try") {
		t.Fatalf("sandbox-on deployment should advertise /v1/try:\n%s", body)
	}

	// ?download=1 attaches as a file.
	dl := httptest.NewRecorder()
	app.handleSkill(dl, httptest.NewRequest(http.MethodGet, "http://m-stash.example/skill.md?download=1", nil))
	if cd := dl.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="SKILL.md"`) {
		t.Fatalf("download disposition = %q", cd)
	}

	// A closed deployment still serves the skill (it is public like llms.txt) but
	// omits the sandbox block.
	closed := newApplication(Config{}, nil, nil)
	recClosed := httptest.NewRecorder()
	closed.handleSkill(recClosed, httptest.NewRequest(http.MethodGet, "http://m-stash.example/skill.md", nil))
	if recClosed.Code != http.StatusOK {
		t.Fatalf("closed /skill.md status = %d, want 200", recClosed.Code)
	}
	if strings.Contains(recClosed.Body.String(), "/v1/try") {
		t.Fatalf("closed deployment must not advertise the sandbox:\n%s", recClosed.Body.String())
	}

	// Non-GET is rejected.
	post := httptest.NewRecorder()
	app.handleSkill(post, httptest.NewRequest(http.MethodPost, "http://m-stash.example/skill.md", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /skill.md status = %d, want 405", post.Code)
	}
}

// Records require visibility; the skill once called private "the default",
// which sent agents straight into a 400.
func TestGenericSkillStatesVisibilityIsRequired(t *testing.T) {
	skill := genericVibeSkill("https://live.example", false)
	if strings.Contains(skill, "the default)") {
		t.Fatalf("skill must not claim a default visibility:\n%s", skill)
	}
	if !strings.Contains(skill, "`visibility` is REQUIRED") || !strings.Contains(skill, "## When a call fails") {
		t.Fatalf("skill must state the record contract and failure fixes:\n%s", skill)
	}
}

// The verify script is pasted into a shell, so model-chosen labels (which may
// contain an apostrophe) must never break out of the single-quoted body.
func TestAppSkillVerifyScriptQuotesBodies(t *testing.T) {
	record := testVibeShareRecord()
	record.PublishableKey = "pk_live_store_123"
	record.Spec.Sections[1].Fields = []vibeField{{Key: "note", Label: "Owner's note", Kind: "text"}}
	skill := vibeAppSkill(record, "https://live.example")
	if !strings.Contains(skill, "## Verify a change") || !strings.Contains(skill, `/v1/me/resources/wishlist`) {
		t.Fatalf("verify section missing:\n%s", skill)
	}
	if !strings.Contains(skill, `owner'\''s note`) {
		t.Fatalf("apostrophe in a body must be shell-escaped:\n%s", skill)
	}
	if !strings.Contains(skill, `"visibility":"private"`) {
		t.Fatalf("member writes must send their visibility:\n%s", skill)
	}
}

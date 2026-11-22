package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	vibeShareCollection = "_m_stash_vibe_shares"
	vibeShareIDBytes    = 9 // 72 bits of entropy, 12 url-safe characters.
	// vibeShareVersion marks the current record shape. Older documents read as
	// expired/not-found so a schema change never serves a half-populated share.
	vibeShareVersion       = 2
	vibeStarterPlaceholder = "pk_your_publishable_key"
)

//go:embed vibe_starter
var vibeStarterFiles embed.FS

// vibeShareRecord is the stored, expiring record for one generated app. It holds
// only the validated spec plus the sandbox's publishable key (safe in the clear)
// — never the owner password, which is returned once at creation and never saved.
type vibeShareRecord struct {
	ID             string    `bson:"_id"`
	Idea           string    `bson:"idea"`
	Spec           vibeSpec  `bson:"spec"`
	SandboxAppID   string    `bson:"sandboxAppId,omitempty"`
	PublishableKey string    `bson:"publishableKey,omitempty"`
	Version        int       `bson:"version"`
	CreatedAt      time.Time `bson:"createdAt"`
	ExpiresAt      time.Time `bson:"expiresAt"`
}

type vibeShare struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	PreviewURL string    `json:"previewUrl,omitempty"`
	ZipURL     string    `json:"zipUrl"`
	SkillURL   string    `json:"skillUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Claimed is true once the sandbox behind this share has been promoted to an
	// owned live app. The UI then hides the expiry and the "keep this app" CTA.
	Claimed bool `json:"claimed,omitempty"`
}

func ensureVibeShareIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(vibeShareCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return fmt.Errorf("create vibe share TTL index: %w", err)
	}
	return nil
}

func newVibeShareID() (string, error) {
	buffer := make([]byte, vibeShareIDBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validVibeShareID(id string) bool {
	if len(id) != base64.RawURLEncoding.EncodedLen(vibeShareIDBytes) {
		return false
	}
	for _, character := range id {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

// vibeShareMaxTTL caps how long any shared vibe (link, live demo, and .zip) can live, so the store never accumulates stale data.
const vibeShareMaxTTL = 5 * 24 * time.Hour

func (app *application) vibeShareTTL() time.Duration {
	ttl := app.config.VibeShareTTL
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	if ttl > vibeShareMaxTTL {
		return vibeShareMaxTTL
	}
	return ttl
}

func (app *application) describeVibeShare(record vibeShareRecord, baseURL string) vibeShare {
	share := vibeShare{
		ID:        record.ID,
		URL:       baseURL + "/vibe/" + record.ID,
		ZipURL:    baseURL + "/v1/vibe/" + record.ID + "/zip",
		SkillURL:  baseURL + "/v1/vibe/" + record.ID + "/skill.md",
		ExpiresAt: record.ExpiresAt,
	}
	// A live demo only exists when a sandbox was provisioned (its publishable key
	// is present). Otherwise the share is spec + downloadable starter only.
	if record.PublishableKey != "" {
		share.PreviewURL = baseURL + "/v1/vibe/" + record.ID + "/preview"
	}
	return share
}

func (app *application) saveVibeShare(ctx context.Context, idea string, spec vibeSpec, sandboxAppID string, publishableKey string, baseURL string) (*vibeShare, error) {
	id, err := newVibeShareID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	record := vibeShareRecord{
		ID:             id,
		Idea:           idea,
		Spec:           spec,
		SandboxAppID:   sandboxAppID,
		PublishableKey: publishableKey,
		Version:        vibeShareVersion,
		CreatedAt:      now,
		ExpiresAt:      now.Add(app.vibeShareTTL()),
	}
	if _, err := app.database.Collection(vibeShareCollection).InsertOne(ctx, record); err != nil {
		return nil, err
	}
	share := app.describeVibeShare(record, baseURL)
	return &share, nil
}

// shareClaimed reports whether the app behind a share's publishable key has been
// claimed into a builder account (it exists and is no longer a sandbox). A missing
// app (never provisioned, or purged after expiry) counts as not claimed.
func (app *application) shareClaimed(ctx context.Context, publishableKey string) bool {
	if publishableKey == "" {
		return false
	}
	var record AppRecord
	if err := app.database.Collection(appsCollectionName).
		FindOne(ctx, bson.M{"publishableKey": publishableKey}).Decode(&record); err != nil {
		return false
	}
	return !record.Sandbox
}

func (app *application) loadVibeShare(ctx context.Context, id string) (*vibeShareRecord, error) {
	var record vibeShareRecord
	err := app.database.Collection(vibeShareCollection).FindOne(ctx, bson.M{
		"_id":       id,
		"version":   vibeShareVersion,
		"expiresAt": bson.M{"$gt": time.Now().UTC()},
	}).Decode(&record)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// handleVibeShare serves /v1/vibe/{id}[/preview|/app.js|/app.css|/views.js|/zip] for a saved, expiring spec.
func (app *application) handleVibeShare(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/vibe/"), "/")
	if !validVibeShareID(id) {
		writeError(w, http.StatusNotFound, "This vibe link doesn't exist or has expired")
		return
	}
	// Static starter assets are identical across shares, so they are served from
	// the embedded files without a database lookup.
	switch action {
	case "app.js":
		app.serveVibeStarterAsset(w, "app.js", "text/javascript; charset=utf-8")
		return
	case "app.css":
		app.serveVibeStarterAsset(w, "app.css", "text/css; charset=utf-8")
		return
	case "views.js":
		app.serveVibeStarterAsset(w, "views.js", "text/javascript; charset=utf-8")
		return
	case "", "preview", "zip", "skill.md":
	default:
		writeError(w, http.StatusNotFound, "API route not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	record, err := app.loadVibeShare(ctx, id)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "This vibe link doesn't exist or has expired")
			return
		}
		app.logger.Error("vibe share lookup failed", "request_id", requestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusServiceUnavailable, "Could not load this vibe right now")
		return
	}
	baseURL := app.publicBaseURL(r)

	switch action {
	case "":
		// Snippets are rendered from the spec plus the live base URL and key at
		// request time, so a share can never carry a snippet pointed at a stored
		// (attacker-chosen) host.
		w.Header().Set("Cache-Control", "no-store")
		share := app.describeVibeShare(*record, baseURL)
		share.Claimed = app.shareClaimed(ctx, record.PublishableKey)
		payload := map[string]any{
			"data":     record.Spec,
			"idea":     record.Idea,
			"share":    share,
			"snippets": renderVibeSnippets(record.Spec, baseURL, record.PublishableKey),
		}
		if record.PublishableKey != "" {
			payload["publishableKey"] = record.PublishableKey
		}
		writeJSON(w, http.StatusOK, payload)
	case "preview":
		if record.PublishableKey == "" {
			writeError(w, http.StatusNotFound, "The live demo is not available for this vibe")
			return
		}
		assetBase := "/v1/vibe/" + record.ID + "/"
		page, err := renderVibeStarterIndex(record, vibeStarterConfig{API: "", App: record.PublishableKey, Mode: "preview"}, assetBase)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not render the live demo")
			return
		}
		// The preview is framed by the vibe page, so it relaxes only frame-ancestors to same-origin.
		w.Header().Set("Content-Security-Policy", vibePreviewCSP(baseURL))
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(page)
	case "zip":
		archive, err := buildVibeStarterZip(record, baseURL, app.shareClaimed(ctx, record.PublishableKey))
		if err != nil {
			app.logger.Error("vibe zip build failed", "request_id", requestIDFromContext(ctx), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not build the starter project")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="m-stash-starter-`+record.ID+`.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(archive)
	case "skill.md":
		// The spec-tailored agent skill: safe to serve for any valid share since it
		// carries only the publishable key and section shapes, never secrets.
		writeSkill(w, r, vibeAppSkill(record, baseURL))
	}
}

// vibePreviewHostPattern is deliberately narrow: whatever we splice into the
// CSP must be a plain host[:port] so a crafted Host header cannot smuggle in
// extra directives or sources.
var vibePreviewHostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$`)

// vibePreviewCSP is the policy for the sandboxed live preview. The preview
// runs in an iframe without allow-same-origin, so its origin is opaque. Chrome
// and Firefox still resolve 'self' against the document URL, but Safari
// (WebKit) resolves it against the opaque origin and blocks the preview's own
// script, stylesheet, and API calls, leaving a blank page. Listing this
// server's origin explicitly keeps the policy just as tight while making the
// preview work everywhere. The scheme is required: WebKit ignores a scheme-less
// host-source here. (An http:// source also matches https:// resources, so a
// proxy that misreports the scheme still works.)
func vibePreviewCSP(baseURL string) string {
	self := "'self'"
	if parsed, err := url.Parse(baseURL); err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		vibePreviewHostPattern.MatchString(parsed.Host) {
		self += " " + parsed.Scheme + "://" + parsed.Host
	}
	return "default-src 'self'; base-uri 'none'; connect-src " + self +
		"; form-action 'none'; frame-ancestors 'self'; img-src 'self' data:; object-src 'none'; script-src " + self +
		"; style-src " + self
}

func (app *application) serveVibeStarterAsset(w http.ResponseWriter, name, contentType string) {
	contents, err := vibeStarterFiles.ReadFile("vibe_starter/" + name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(contents)
}

// vibeStarterConfig is the JSON handed to the starter runtime through the inline
// <script id="m-stash-config"> block. It carries the API base, publishable key,
// render mode, and the spec's presentation — but never samples, so the client
// only ever renders the live records it reads back from the API.
type vibeStarterConfig struct {
	API      string        `json:"api"`
	App      string        `json:"app"`
	Mode     string        `json:"mode"`
	Name     string        `json:"name"`
	Tagline  string        `json:"tagline"`
	Accent   string        `json:"accent"`
	Icon     string        `json:"icon"`
	Sections []vibeSection `json:"sections"`
}

// specSectionsForClient copies the spec's sections with their seeded samples
// stripped, so the generated app never renders stored sample data; it reads the
// live sandbox records instead.
func specSectionsForClient(spec vibeSpec) []vibeSection {
	sections := make([]vibeSection, 0, len(spec.Sections))
	for _, section := range spec.Sections {
		section.Samples = nil
		sections = append(sections, section)
	}
	return sections
}

func renderVibeStarterIndex(record *vibeShareRecord, config vibeStarterConfig, assetBase string) ([]byte, error) {
	template, err := vibeStarterFiles.ReadFile("vibe_starter/index.html")
	if err != nil {
		return nil, err
	}
	config.Name = record.Spec.Name
	config.Tagline = record.Spec.Tagline
	config.Accent = record.Spec.Accent
	config.Icon = record.Spec.Icon
	config.Sections = specSectionsForClient(record.Spec)
	// json.Marshal escapes <, >, and & so the payload cannot close its <script> block.
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	replacer := strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(clampString(record.Spec.Name, 60)),
		"{{ASSET_BASE}}", html.EscapeString(assetBase),
		"{{CONFIG}}", string(configJSON),
	)
	return []byte(replacer.Replace(string(template))), nil
}

func buildVibeStarterZip(record *vibeShareRecord, baseURL string, claimed bool) ([]byte, error) {
	// The downloaded starter runs against the live sandbox when one exists; if the
	// playground was full, it ships with the placeholder key for the user to swap.
	publishableKey := record.PublishableKey
	if publishableKey == "" {
		publishableKey = vibeStarterPlaceholder
	}
	index, err := renderVibeStarterIndex(record, vibeStarterConfig{API: baseURL, App: publishableKey, Mode: "starter"}, "")
	if err != nil {
		return nil, err
	}
	appJS, err := vibeStarterFiles.ReadFile("vibe_starter/app.js")
	if err != nil {
		return nil, err
	}
	appCSS, err := vibeStarterFiles.ReadFile("vibe_starter/app.css")
	if err != nil {
		return nil, err
	}
	viewsJS, err := vibeStarterFiles.ReadFile("vibe_starter/views.js")
	if err != nil {
		return nil, err
	}
	specJSON, err := json.MarshalIndent(record.Spec, "", "  ")
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := []struct {
		name     string
		contents []byte
	}{
		{"README.md", []byte(vibeStarterReadme(record, baseURL, claimed))},
		{"AGENTS.md", []byte(vibeStarterAgents(record, baseURL))},
		{"SKILL.md", []byte(vibeAppSkill(record, baseURL))},
		{"index.html", index},
		{"app.js", appJS},
		{"app.css", appCSS},
		{"views.js", viewsJS},
		{"mstash.json", specJSON},
	}
	for _, file := range files {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: "m-stash-starter/" + file.name, Method: zip.Deflate, Modified: record.CreatedAt})
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(file.contents); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// sectionAudienceSummary explains, in app-owner language, who may write and
// read a section's records.
func sectionAudienceSummary(section vibeSection) string {
	switch section.Audience {
	case vibeAudienceOwner:
		return "the app owner writes these; everyone can read them without signing in"
	case vibeAudienceMemberPublic:
		return "each signed-in user owns these and they are public on their page"
	default:
		return "each signed-in user keeps these privately to themselves"
	}
}

func vibeStarterReadme(record *vibeShareRecord, baseURL string, claimed bool) string {
	spec := record.Spec
	name := spec.Name
	if name == "" {
		name = "Your app"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	if spec.Tagline != "" {
		fmt.Fprintf(&b, "> %s\n\n", spec.Tagline)
	}
	fmt.Fprintf(&b, "A ready-to-run starter the m-stash vibe lab generated from this idea:\n\n> %s\n\n", record.Idea)
	if claimed {
		b.WriteString("It's plain HTML, CSS, and JavaScript — no build step, no install. It points at your **live m-stash app**, so it works the moment you open it: browse the public pages, create a user account, or sign in as the app owner to manage the shared data.\n\n")
	} else {
		b.WriteString("It's plain HTML, CSS, and JavaScript — no build step, no install. It already points at a **live m-stash sandbox**, so it works the moment you open it: browse the public pages, create a user account, or sign in as the app owner to manage the shared data.\n\n")
	}

	b.WriteString("## Run it\n\n")
	b.WriteString("Just serve this folder and open `index.html`:\n\n")
	b.WriteString("```sh\nnpx serve .\n# or\npython3 -m http.server 8000\n```\n\n")
	b.WriteString("Then visit the printed URL. Opening `index.html` directly from disk works too.\n\n")

	b.WriteString("## What's inside\n\n")
	for _, section := range spec.Sections {
		title := section.Title
		if title == "" {
			title = section.Type
		}
		fmt.Fprintf(&b, "- **%s** (`%s`, %s view) — %s.\n", title, section.Type, section.View, sectionAudienceSummary(section))
	}
	b.WriteString("\n")

	if claimed {
		// A claimed app is permanent and already belongs to the builder, so the
		// zip drops the "this sandbox expires" guidance entirely.
		b.WriteString("## It's yours now\n\n")
		b.WriteString("This app has been claimed into your m-stash console, so it no longer expires and the publishable key in this folder points at your live app. Manage it, rotate its secret, and set allowed origins at the console.\n\n")
		b.WriteString("Nothing secret lives in this folder. The publishable key only names an app; m-stash enforces ownership and visibility on the server for every request, so it is safe to ship in frontend code.\n\n")
	} else {
		b.WriteString("## Keep it past the sandbox\n\n")
		if !record.ExpiresAt.IsZero() {
			fmt.Fprintf(&b, "The live sandbox behind this starter is temporary — it is deleted on **%s**. To keep your app running after that, claim it into a free builder account — the publishable key stays the same, so this folder keeps working with no changes:\n\n", record.ExpiresAt.UTC().Format("Mon, 02 Jan 2006 15:04 MST"))
		} else {
			b.WriteString("The live sandbox behind this starter is temporary. To keep your app running, claim it into a free builder account — the publishable key stays the same, so this folder keeps working with no changes:\n\n")
		}
		fmt.Fprintf(&b, "1. Open %s/console and claim this app (or paste its publishable key). You keep every user and record already created.\n", baseURL)
		b.WriteString("2. That's it — the same `pk_…` now points at your own live app, under your control.\n")
		b.WriteString("3. (Optional) Set allowed origins and rotate the secret key from the console.\n\n")
		b.WriteString("Nothing secret lives in this folder. The publishable key only names an app; m-stash enforces ownership and visibility on the server for every request, so it is safe to ship in frontend code.\n\n")
	}

	b.WriteString("## Data model\n\n| Section | Type | Who writes | Visibility | View |\n| --- | --- | --- | --- | --- |\n")
	for _, section := range spec.Sections {
		title := section.Title
		if title == "" {
			title = section.Type
		}
		writer := "customer"
		if section.Audience == vibeAudienceOwner {
			writer = "owner"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", title, section.Type, writer, section.visibility(), section.View)
	}
	b.WriteString("\n")

	if snippets := renderVibeSnippets(spec, baseURL, record.PublishableKey); len(snippets) > 0 {
		b.WriteString("## Talk to the API\n\nThese call the live sandbox. Copy, paste, and build.\n\n")
		for _, snippet := range snippets {
			language := snippet.Language
			if language == "" {
				language = "javascript"
			}
			fmt.Fprintf(&b, "### %s\n\n```%s\n%s\n```\n\n", snippet.Title, language, snippet.Code)
		}
	}

	b.WriteString("## Hand it to your coding agent\n\n")
	b.WriteString("`SKILL.md` is a plug-and-play agent skill pre-filled with this app's key and sections. Drop it in so Cursor, Claude Code, or Codex can build against m-stash with zero setup:\n\n")
	b.WriteString("- Cursor: `.cursor/skills/m-stash/SKILL.md`\n")
	b.WriteString("- Claude Code: `.claude/skills/m-stash/SKILL.md`\n\n")
	fmt.Fprintf(&b, "`AGENTS.md` has the full set of API rules a coding agent should follow, and the machine-readable reference lives at %s/llms.txt.\n", baseURL)
	return b.String()
}

// vibeStarterAgents gives a coding agent (Cursor, Claude, ...) the rules it needs
// to keep extending the starter against m-stash without breaking ownership.
func vibeStarterAgents(record *vibeShareRecord, baseURL string) string {
	spec := record.Spec
	var b strings.Builder
	b.WriteString("# Working on this m-stash app\n\n")
	b.WriteString("This is a vanilla HTML/CSS/JS app backed by **m-stash**, an API that provides authentication plus per-user and app-wide data, with ownership enforced on the server. Follow these rules so changes stay safe and keep working.\n\n")

	b.WriteString("## The golden rules\n\n")
	b.WriteString("- Every API request must send the header `X-M-Stash-App: <publishable key>`. This app's key is in the `m-stash-config` block of `index.html`.\n")
	b.WriteString("- The publishable key (`pk_…`) is safe in frontend code; it only names the app. **Never** put a secret key (`sk_…`), password, or token in frontend/source files.\n")
	b.WriteString("- Authenticate end users with `Authorization: Bearer <token>` from signup/login. Use `credentials: \"omit\"` on fetch.\n")
	b.WriteString("- Ownership and visibility are enforced by the server. Don't try to work around a 401/403 on the client — fix the auth or the visibility instead.\n\n")

	b.WriteString("## Endpoints\n\n")
	fmt.Fprintf(&b, "Base URL: `%s`\n\n", baseURL)
	b.WriteString("- `POST /v1/auth/signup` — body `{ email, password, username }`; returns `{ token, user, namespace }`. Creates a customer account.\n")
	b.WriteString("- `POST /v1/auth/login` — body `{ email, password }`; returns `{ token, user }`. The owner (admin) signs in here too.\n")
	b.WriteString("- `GET|POST /v1/me/resources/{type}` and `GET|PUT|DELETE /v1/me/resources/{type}/{slug}` — the signed-in user's **personal** records. Private by default.\n")
	b.WriteString("- `GET|POST /v1/shared/resources/{type}` and `GET|PUT|DELETE /v1/shared/resources/{type}/{slug}` — **app-wide** records. Only an admin (or your backend's secret key) may write.\n")
	b.WriteString("- `GET /v1/public/shared/resources/{type}[/{slug}]` — public app-wide records, no login required.\n")
	b.WriteString("- `GET /v1/public/{username}/{type}[/{slug}]` — a user's public records on their own page.\n\n")

	b.WriteString("## Visibility\n\n")
	b.WriteString("- `private` — only the owning user can read it (customer wishlists, saves).\n")
	b.WriteString("- `authenticated` — any signed-in user can read it (shared resources only).\n")
	b.WriteString("- `public` — anyone can read it, even logged out (the catalog, announcements).\n\n")

	b.WriteString("## This app's sections\n\n")
	for _, section := range spec.Sections {
		title := section.Title
		if title == "" {
			title = section.Type
		}
		scope := "/v1/me/resources/"
		if section.Audience == vibeAudienceOwner {
			scope = "/v1/shared/resources/"
		}
		fields := make([]string, 0, len(section.Fields))
		for _, field := range section.Fields {
			fields = append(fields, field.Key+":"+field.Kind)
		}
		fmt.Fprintf(&b, "- **%s** → `%s%s` (visibility `%s`). Fields: %s.\n", title, scope, section.Type, section.visibility(), strings.Join(fields, ", "))
	}
	b.WriteString("\n")

	b.WriteString("## A record body\n\n")
	b.WriteString("```json\n{\n  \"slug\": \"unique-slug\",\n  \"title\": \"Human title\",\n  \"visibility\": \"public\",\n  \"data\": { \"fieldKey\": \"value\" }\n}\n```\n\n")
	fmt.Fprintf(&b, "Full machine-readable reference: %s/llms.txt\n", baseURL)
	return b.String()
}

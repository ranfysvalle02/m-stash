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
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	vibeShareCollection   = "_m_stash_vibe_shares"
	vibeShareIDBytes      = 9
	vibeStarterPlaceholder = "pk_your_publishable_key"
)

//go:embed vibe_starter
var vibeStarterFiles embed.FS

type vibeShareRecord struct {
	ID        string    `bson:"_id"`
	Idea      string    `bson:"idea"`
	Plan      vibePlan  `bson:"plan"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

type vibeShare struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	PreviewURL string    `json:"previewUrl,omitempty"`
	ZipURL     string    `json:"zipUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
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

func (app *application) vibeShareTTL() time.Duration {
	if app.config.VibeShareTTL > 0 {
		return app.config.VibeShareTTL
	}
	return 72 * time.Hour
}

func (app *application) describeVibeShare(record vibeShareRecord, baseURL string) vibeShare {
	share := vibeShare{
		ID:        record.ID,
		URL:       baseURL + "/vibe/" + record.ID,
		ZipURL:    baseURL + "/v1/vibe/" + record.ID + "/zip",
		ExpiresAt: record.ExpiresAt,
	}
	if app.uiAppKey != "" {
		share.PreviewURL = baseURL + "/v1/vibe/" + record.ID + "/preview"
	}
	return share
}

func (app *application) saveVibeShare(ctx context.Context, idea string, plan *vibePlan, baseURL string) (*vibeShare, error) {
	id, err := newVibeShareID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	record := vibeShareRecord{ID: id, Idea: idea, Plan: *plan, CreatedAt: now, ExpiresAt: now.Add(app.vibeShareTTL())}
	if _, err := app.database.Collection(vibeShareCollection).InsertOne(ctx, record); err != nil {
		return nil, err
	}
	share := app.describeVibeShare(record, baseURL)
	return &share, nil
}

func (app *application) loadVibeShare(ctx context.Context, id string) (*vibeShareRecord, error) {
	var record vibeShareRecord
	err := app.database.Collection(vibeShareCollection).FindOne(ctx, bson.M{"_id": id, "expiresAt": bson.M{"$gt": time.Now().UTC()}}).Decode(&record)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// handleVibeShare serves /v1/vibe/{id}[/preview|/app.js|/app.css|/zip] for a saved, expiring plan.
func (app *application) handleVibeShare(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/vibe/"), "/")
	if !validVibeShareID(id) {
		writeError(w, http.StatusNotFound, "This vibe link doesn't exist or has expired")
		return
	}
	switch action {
	case "app.js":
		app.serveVibeStarterAsset(w, "app.js", "text/javascript; charset=utf-8")
		return
	case "app.css":
		app.serveVibeStarterAsset(w, "app.css", "text/css; charset=utf-8")
		return
	case "", "preview", "zip":
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
		writeJSON(w, http.StatusOK, map[string]any{"data": record.Plan, "idea": record.Idea, "share": app.describeVibeShare(*record, baseURL)})
	case "preview":
		if app.uiAppKey == "" {
			writeError(w, http.StatusNotFound, "The live demo is not available on this deployment")
			return
		}
		assetBase := "/v1/vibe/" + record.ID + "/"
		page, err := renderVibeStarterIndex(record, vibeStarterConfig{API: "", App: app.uiAppKey, Throwaway: true}, assetBase)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not render the live demo")
			return
		}
		// The preview is framed by the vibe page, so it relaxes only frame-ancestors to same-origin.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; form-action 'none'; frame-ancestors 'self'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(page)
	case "zip":
		archive, err := buildVibeStarterZip(record, baseURL)
		if err != nil {
			app.logger.Error("vibe zip build failed", "request_id", requestIDFromContext(ctx), "error", err)
			writeError(w, http.StatusInternalServerError, "Could not build the starter project")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="m-stash-starter-`+record.ID+`.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(archive)
	}
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

type vibeStarterConfig struct {
	API       string          `json:"api"`
	App       string          `json:"app"`
	Throwaway bool            `json:"throwaway"`
	Idea      string          `json:"idea"`
	Summary   string          `json:"summary"`
	Parts     []vibeModelPart `json:"parts"`
}

// starterParts keeps only pieces the live API will accept, so the generated app never posts to an invalid route.
func starterParts(plan vibePlan) []vibeModelPart {
	parts := []vibeModelPart{}
	seen := map[string]bool{}
	for _, part := range plan.DataModel {
		if validateResourceType(part.Type) != nil || seen[part.Scope+"/"+part.Type] {
			continue
		}
		seen[part.Scope+"/"+part.Type] = true
		parts = append(parts, part)
	}
	return parts
}

func renderVibeStarterIndex(record *vibeShareRecord, config vibeStarterConfig, assetBase string) ([]byte, error) {
	template, err := vibeStarterFiles.ReadFile("vibe_starter/index.html")
	if err != nil {
		return nil, err
	}
	config.Idea = record.Idea
	config.Summary = record.Plan.Summary
	config.Parts = starterParts(record.Plan)
	// json.Marshal escapes <, >, and & so the payload cannot close its <script> block.
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	replacer := strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(clampString(record.Idea, 80)),
		"{{ASSET_BASE}}", html.EscapeString(assetBase),
		"{{CONFIG}}", string(configJSON),
	)
	return []byte(replacer.Replace(string(template))), nil
}

func buildVibeStarterZip(record *vibeShareRecord, baseURL string) ([]byte, error) {
	index, err := renderVibeStarterIndex(record, vibeStarterConfig{API: baseURL, App: vibeStarterPlaceholder}, "")
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
	planJSON, err := json.MarshalIndent(map[string]any{"idea": record.Idea, "plan": record.Plan}, "", "  ")
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := []struct {
		name     string
		contents []byte
	}{
		{"README.md", []byte(vibeStarterReadme(record, baseURL))},
		{"index.html", index},
		{"app.js", appJS},
		{"app.css", appCSS},
		{"plan.json", planJSON},
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

func vibeStarterReadme(record *vibeShareRecord, baseURL string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# m-stash starter\n\n> %s\n\n", record.Idea)
	if record.Plan.Summary != "" {
		fmt.Fprintf(&builder, "%s\n\n", record.Plan.Summary)
	}
	builder.WriteString("## Run it\n\n")
	fmt.Fprintf(&builder, "1. Get a publishable key (`pk_…`) for your app from %s/console.\n", baseURL)
	builder.WriteString("2. Open `index.html`, find the `m-stash-config` block, and replace `" + vibeStarterPlaceholder + "` with your key.\n")
	builder.WriteString("3. Serve the folder (for example `npx serve .`) and open it. Add that origin to your app's allowed origins if you restricted them.\n\n")
	builder.WriteString("No secrets live in this folder: the publishable key only names your app. Ownership is enforced by m-stash on every request.\n\n")
	if len(record.Plan.DataModel) > 0 {
		builder.WriteString("## Data model\n\n| Name | Scope | Type | Visibility | Purpose |\n| --- | --- | --- | --- | --- |\n")
		for _, part := range record.Plan.DataModel {
			fmt.Fprintf(&builder, "| %s | %s | `%s` | %s | %s |\n", part.Name, part.Scope, part.Type, part.Visibility, strings.ReplaceAll(part.Purpose, "|", "/"))
		}
		builder.WriteString("\n")
	}
	if len(record.Plan.Snippets) > 0 {
		builder.WriteString("## Snippets\n\n")
		for _, snippet := range record.Plan.Snippets {
			language := snippet.Language
			if language == "" {
				language = "javascript"
			}
			fmt.Fprintf(&builder, "### %s\n\n```%s\n%s\n```\n\n", snippet.Title, language, snippet.Code)
		}
	}
	if record.Plan.WhyMStash != "" {
		fmt.Fprintf(&builder, "## Why m-stash\n\n%s\n", record.Plan.WhyMStash)
	}
	return builder.String()
}

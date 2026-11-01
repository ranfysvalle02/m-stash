package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	vibeMaxIdeaLength  = 600
	vibeRequestTimeout = 25 * time.Second
	geminiAPIBase      = "https://generativelanguage.googleapis.com/v1beta/models/"
)

// vibeHTTPClient is dedicated to the upstream model call so its timeout is independent of the request context.
var vibeHTTPClient = &http.Client{Timeout: vibeRequestTimeout}

func (app *application) vibeEnabled() bool {
	return strings.TrimSpace(app.config.GeminiAPIKey) != ""
}

type vibeRequest struct {
	Idea string `json:"idea"`
}

// vibePlan is the structured shape Gemini must return and the API hands back; nothing here is ever executed.
type vibePlan struct {
	Summary   string          `json:"summary"`
	DataModel []vibeModelPart `json:"dataModel"`
	Snippets  []vibeSnippet   `json:"snippets"`
	WhyMStash string          `json:"whyMStash"`
}

type vibeModelPart struct {
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	Type       string `json:"type"`
	Visibility string `json:"visibility"`
	Purpose    string `json:"purpose"`
}

type vibeSnippet struct {
	Title    string `json:"title"`
	Language string `json:"language"`
	Code     string `json:"code"`
}

func validateVibeIdea(idea string) (string, error) {
	trimmed := strings.TrimSpace(idea)
	if len(trimmed) < 3 {
		return "", fmt.Errorf("describe your app idea in a sentence or two")
	}
	if len(trimmed) > vibeMaxIdeaLength {
		return "", fmt.Errorf("keep the idea under %d characters", vibeMaxIdeaLength)
	}
	return trimmed, nil
}

const vibeSystemInstruction = `You are a senior engineer who designs backends on m-stash, a small API that gives an app authentication plus per-user and app-wide data on MongoDB, with ownership enforced server-side.

m-stash API facts you must use and must not contradict:
- Every request sends the header "X-M-Stash-App: <publishableKey>".
- Users sign up in the builder's own app: POST /v1/auth/signup { email, password, username }. Login: POST /v1/auth/login.
- Personal data (owned by the signed-in user): POST or GET /v1/me/resources/{type} and /v1/me/resources/{type}/{slug}. Body: { slug, title, data, visibility }.
- Shared app data (owned by the deployment/admin): GET /v1/shared/resources/{type}; writes require an admin or the app secret key.
- Public reads (no auth): GET /v1/public/shared/resources/{type}/{slug} and GET /v1/public/{username}/{type}/{slug}.
- A "type" is a bucket (e.g. config, save, profile, leaderboard). "visibility" is per record: "private" (owner only), "authenticated" (any signed-in user), or "public" (anyone).
- MongoDB is never exposed to the browser. The browser never chooses an owner.

Given an app idea, design the smallest m-stash backend for it. Be concrete and correct. Keep snippets short, runnable browser fetch() calls against the real endpoints above. Never invent endpoints. Never include secrets. Respond ONLY with JSON matching the provided schema.`

func buildVibeUserPrompt(idea string) string {
	return "App idea:\n" + idea + "\n\nDesign the m-stash backend: a one-paragraph summary, the data model (each piece with scope personal|shared, a type, a visibility, and its purpose), two or three copy-paste fetch() snippets, and one sentence on why m-stash fits."
}

func vibeResponseSchema() map[string]any {
	stringField := map[string]any{"type": "STRING"}
	return map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"summary": stringField,
			"dataModel": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"name":       stringField,
						"scope":      stringField,
						"type":       stringField,
						"visibility": stringField,
						"purpose":    stringField,
					},
				},
			},
			"snippets": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"title":    stringField,
						"language": stringField,
						"code":     stringField,
					},
				},
			},
			"whyMStash": stringField,
		},
	}
}

func clampString(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return strings.TrimSpace(value[:max])
	}
	return value
}

// sanitizeVibePlan bounds every field so a misbehaving model response can never bloat the payload.
func sanitizeVibePlan(plan vibePlan) vibePlan {
	cleaned := vibePlan{
		Summary:   clampString(plan.Summary, 600),
		WhyMStash: clampString(plan.WhyMStash, 400),
	}
	for index, part := range plan.DataModel {
		if index >= 8 {
			break
		}
		cleaned.DataModel = append(cleaned.DataModel, vibeModelPart{
			Name:       clampString(part.Name, 80),
			Scope:      clampString(part.Scope, 20),
			Type:       clampString(part.Type, 40),
			Visibility: clampString(part.Visibility, 20),
			Purpose:    clampString(part.Purpose, 200),
		})
	}
	for index, snippet := range plan.Snippets {
		if index >= 4 {
			break
		}
		cleaned.Snippets = append(cleaned.Snippets, vibeSnippet{
			Title:    clampString(snippet.Title, 80),
			Language: clampString(snippet.Language, 20),
			Code:     clampString(snippet.Code, 2000),
		})
	}
	return cleaned
}

func (app *application) handleVibe(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !app.vibeEnabled() {
		writeError(w, http.StatusNotFound, "The vibe lab is not enabled on this deployment")
		return
	}

	var input vibeRequest
	if !decodeJSONBody(w, r, &input, false) {
		return
	}
	idea, err := validateVibeIdea(input.Idea)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), vibeRequestTimeout)
	defer cancel()

	// Reject per-IP abuse before spending from the shared daily budget.
	if ok, limitErr := app.vibeLimiter.allow(ctx, "vibe-ip", clientIP(r, app.config.TrustProxy)); limitErr != nil {
		app.logger.Error("vibe per-ip limit check failed", "request_id", requestIDFromContext(ctx), "error", limitErr)
		writeError(w, http.StatusServiceUnavailable, "The vibe lab is catching its breath")
		return
	} else if !ok {
		writeError(w, http.StatusTooManyRequests, "Easy, tiger — too many vibes. Try again shortly.")
		return
	}
	if ok, limitErr := app.vibeDailyLimiter.allow(ctx, "vibe-global", "global"); limitErr != nil {
		app.logger.Error("vibe daily limit check failed", "request_id", requestIDFromContext(ctx), "error", limitErr)
		writeError(w, http.StatusServiceUnavailable, "The vibe lab is catching its breath")
		return
	} else if !ok {
		writeError(w, http.StatusTooManyRequests, "The vibe lab hit its daily limit — back tomorrow")
		return
	}

	plan, err := app.generateVibePlan(ctx, idea)
	if err != nil {
		app.logger.Error("vibe generation failed", "request_id", requestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusBadGateway, "The vibe lab couldn't dream that one up — try rephrasing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plan})
}

func (app *application) generateVibePlan(ctx context.Context, idea string) (*vibePlan, error) {
	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]any{{"text": vibeSystemInstruction}},
		},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": buildVibeUserPrompt(idea)}}},
		},
		"generationConfig": map[string]any{
			"temperature":      0.6,
			"maxOutputTokens":  1200,
			"responseMimeType": "application/json",
			"responseSchema":   vibeResponseSchema(),
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(app.config.GeminiModel)
	if model == "" {
		model = "gemini-flash-latest"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiAPIBase+model+":generateContent", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	// Pass the key as a header so it never appears in a URL or proxy log.
	request.Header.Set("x-goog-api-key", app.config.GeminiAPIKey)

	response, err := vibeHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini returned status %d: %s", response.StatusCode, clampString(string(raw), 300))
	}
	return parseVibeResponse(raw)
}

func parseVibeResponse(raw []byte) (*vibePlan, error) {
	var envelope struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode model envelope: %w", err)
	}
	if len(envelope.Candidates) == 0 || len(envelope.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("model returned no content")
	}
	text := strings.TrimSpace(envelope.Candidates[0].Content.Parts[0].Text)
	var plan vibePlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return nil, fmt.Errorf("decode model plan: %w", err)
	}
	cleaned := sanitizeVibePlan(plan)
	if cleaned.Summary == "" && len(cleaned.DataModel) == 0 && len(cleaned.Snippets) == 0 {
		return nil, fmt.Errorf("model plan was empty")
	}
	return &cleaned, nil
}

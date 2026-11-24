package main

import (
	"embed"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Tutorials are plain Markdown files in tutorials/, embedded at build time so
// contributors edit docs, not code. Each starts with a small frontmatter block
// (title, summary, minutes, level, order). "{{API}}" in the body is replaced
// with this deployment's public origin, so every snippet runs as pasted.

//go:embed tutorials/*.md
var tutorialFiles embed.FS

type tutorial struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Minutes int    `json:"minutes"`
	Level   string `json:"level"`
	Order   int    `json:"-"`
	body    string
}

var (
	tutorialsOnce   sync.Once
	tutorialsLoaded []tutorial
	tutorialsErr    error
)

// loadTutorials parses every embedded tutorial once, sorted by order.
func loadTutorials() ([]tutorial, error) {
	tutorialsOnce.Do(func() {
		entries, err := tutorialFiles.ReadDir("tutorials")
		if err != nil {
			tutorialsErr = err
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			raw, err := tutorialFiles.ReadFile("tutorials/" + entry.Name())
			if err != nil {
				tutorialsErr = err
				return
			}
			parsed, err := parseTutorial(strings.TrimSuffix(entry.Name(), ".md"), string(raw))
			if err != nil {
				tutorialsErr = fmt.Errorf("tutorial %s: %w", entry.Name(), err)
				return
			}
			tutorialsLoaded = append(tutorialsLoaded, parsed)
		}
		sort.SliceStable(tutorialsLoaded, func(i, j int) bool { return tutorialsLoaded[i].Order < tutorialsLoaded[j].Order })
	})
	return tutorialsLoaded, tutorialsErr
}

func parseTutorial(slug, raw string) (tutorial, error) {
	if validateResourceSlug(slug) != nil {
		return tutorial{}, fmt.Errorf("file name must be a lowercase-hyphen slug")
	}
	rest, ok := strings.CutPrefix(strings.ReplaceAll(raw, "\r\n", "\n"), "---\n")
	if !ok {
		return tutorial{}, fmt.Errorf("missing frontmatter")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return tutorial{}, fmt.Errorf("unterminated frontmatter")
	}
	parsed := tutorial{Slug: slug, body: strings.TrimLeft(body, "\n")}
	for _, line := range strings.Split(front, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "title":
			parsed.Title = value
		case "summary":
			parsed.Summary = value
		case "level":
			parsed.Level = value
		case "minutes":
			parsed.Minutes, _ = strconv.Atoi(value)
		case "order":
			parsed.Order, _ = strconv.Atoi(value)
		}
	}
	if parsed.Title == "" || parsed.Summary == "" || parsed.Minutes <= 0 {
		return tutorial{}, fmt.Errorf("frontmatter needs title, summary, and minutes")
	}
	return parsed, nil
}

func findTutorial(slug string) (tutorial, bool) {
	tutorials, err := loadTutorials()
	if err != nil {
		return tutorial{}, false
	}
	for _, candidate := range tutorials {
		if candidate.Slug == slug {
			return candidate, true
		}
	}
	return tutorial{}, false
}

func (t tutorial) markdown(baseURL string) string {
	return strings.ReplaceAll(t.body, "{{API}}", baseURL)
}

// handleTutorials serves GET /v1/tutorials (the index), /v1/tutorials/{slug}
// (JSON with the stamped Markdown), and /v1/tutorials/{slug}.md (raw Markdown,
// for agents and curl).
func (app *application) handleTutorials(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	tutorials, err := loadTutorials()
	if err != nil {
		app.logger.Error("tutorials failed to load", "error", err)
		writeError(w, http.StatusInternalServerError, "Tutorials are unavailable")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tutorials"), "/")
	if rest == "" {
		writeJSON(w, http.StatusOK, map[string]any{"data": tutorials})
		return
	}
	slug, raw := strings.CutSuffix(path.Base(rest), ".md")
	found, ok := findTutorial(slug)
	if !ok || strings.Contains(rest, "/") {
		writeError(w, http.StatusNotFound, "Tutorial not found")
		return
	}
	markdown := found.markdown(app.publicBaseURL(r))
	if raw {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(markdown))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"slug": found.Slug, "title": found.Title, "summary": found.Summary,
		"minutes": found.Minutes, "level": found.Level, "markdown": markdown,
	}})
}

// handleClientScript serves the zero-dependency mstash.js client, so a page
// can import it straight from the deployment with no install step. It is a
// public, credential-free static file, so any origin may load it.
func (app *application) handleClientScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	contents, err := vibeStarterFiles.ReadFile("vibe_starter/mstash.js")
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(contents)
}

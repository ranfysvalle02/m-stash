package main

import (
	"bytes"
	"embed"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:frontend/dist
var embeddedFrontend embed.FS

func (app *application) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if strings.HasPrefix(r.URL.Path, "/v1/") {
		writeError(w, http.StatusNotFound, "API route not found")
		return
	}

	requestedPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requestedPath != "" && requestedPath != "index.html" && fs.ValidPath(requestedPath) {
		if serveEmbeddedFrontendFile(w, r, requestedPath) {
			return
		}
		if path.Ext(requestedPath) != "" {
			http.NotFound(w, r)
			return
		}
	}

	app.serveIndexHTML(w, r)
}

// serveIndexHTML injects the UI's publishable key so the bundled SPA knows which
// tenant to call without any build-time or manual configuration.
func (app *application) serveIndexHTML(w http.ResponseWriter, r *http.Request) {
	contents, err := embeddedFrontend.ReadFile("frontend/dist/index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if app.uiAppKey != "" {
		meta := `<meta name="m-stash-app" content="` + html.EscapeString(app.uiAppKey) + `" />`
		contents = bytes.Replace(contents, []byte("</head>"), []byte("    "+meta+"\n  </head>"), 1)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(contents))
}

func serveEmbeddedFrontendFile(w http.ResponseWriter, r *http.Request, filePath string) bool {
	contents, err := embeddedFrontend.ReadFile("frontend/dist/" + filePath)
	if err != nil {
		return false
	}

	if strings.HasPrefix(filePath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, filePath, time.Time{}, bytes.NewReader(contents))
	return true
}

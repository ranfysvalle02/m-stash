package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	seoSiteName           = "m-stash"
	seoDefaultTitle       = "m-stash — Auth and user data for AI-built apps"
	seoDefaultDescription = "Give AI-built apps sign-up, login, and server-enforced per-user data without wiring a database or writing security rules. Self-hostable, MongoDB-backed API."
	seoImageAlt           = "m-stash — auth and per-user data for AI-built apps"
	seoLookupTimeout      = 1500 * time.Millisecond
	seoSitemapLimit       = 500
	seoDescriptionLimit   = 200
)

var (
	seoBlockStart = []byte("<!-- seo:start -->")
	seoBlockEnd   = []byte("<!-- seo:end -->")
)

type pageMeta struct {
	Title       string
	Description string
	// Path is the canonical path for the page; empty means the request path.
	Path    string
	Type    string
	NoIndex bool
}

var staticPageMeta = map[string]pageMeta{
	"/discover": {Title: seoDefaultTitle, Description: seoDefaultDescription},
	"/guide": {
		Title:       "Build guide: ship the app, skip the backend setup — m-stash",
		Description: "Add sign-up, login, and server-enforced user ownership to the app you're building. No database access in the frontend, no security rules to write.",
	},
	"/vibe": {
		Title:       "Vibe lab: describe your app, get its backend — m-stash",
		Description: "Tell m-stash what you're building. It sketches the data model and hands you copy-paste code grounded in the real API. No account needed.",
	},
	"/public": {
		Title:       "Live public data — m-stash",
		Description: "Every record here is a live m-stash API response: the same backend an AI-built app calls for auth and per-user data.",
	},
	"/tutorials": {
		Title:       "Tutorials: build on m-stash in minutes — m-stash",
		Description: "Step-by-step tutorials: your first app in five minutes, login and private data on any page, public profiles and counters, a backend with your secret key, and building with your AI agent.",
	},
	"/faq": {
		Title:       "FAQ — m-stash",
		Description: "Builder-first answers about m-stash: auth, per-user data, ownership, self-hosting, and the hosted demo.",
	},
	"/privacy": {
		Title:       "Privacy Policy — m-stash",
		Description: "What the hosted m-stash demo stores, why, and how long it is kept.",
	},
	"/console": {Title: "Builder console — m-stash", Description: seoDefaultDescription, NoIndex: true},
	"/admin":   {Title: "Operator console — m-stash", Description: seoDefaultDescription, NoIndex: true},
	"/login":   {Title: "Log in — m-stash", Description: seoDefaultDescription, NoIndex: true},
	"/signup":  {Title: "Sign up — m-stash", Description: seoDefaultDescription, NoIndex: true},
}

// sitemapStaticPaths lists the indexable top-level pages in priority order.
var sitemapStaticPaths = []string{"/discover", "/guide", "/tutorials", "/vibe", "/public", "/faq", "/privacy"}

// redirectToCanonicalHost permanently redirects browser pages served on a
// secondary host (for example the platform's default *.onrender.com domain or
// www.) to PUBLIC_BASE_URL so shared links and search results converge on one origin.
// API routes are never redirected because CORS preflights cannot follow them.
func (app *application) redirectToCanonicalHost(w http.ResponseWriter, r *http.Request) bool {
	configured := strings.TrimRight(strings.TrimSpace(app.config.PublicBaseURL), "/")
	if configured == "" {
		return false
	}
	canonical, err := url.Parse(configured)
	if err != nil || canonical.Host == "" || strings.EqualFold(r.Host, canonical.Host) || isLoopbackHost(r.Host) {
		return false
	}
	target := configured + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
	return true
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if splitHost, _, err := net.SplitHostPort(hostport); err == nil {
		host = splitHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pageMetaFor resolves the title, description, and indexing policy for a SPA
// route. Public records are looked up so link previews show their real title.
func (app *application) pageMetaFor(ctx context.Context, requestPath string) pageMeta {
	cleanPath := path.Clean("/" + requestPath)
	if cleanPath == "/" {
		meta := staticPageMeta["/discover"]
		meta.Path = "/discover"
		return meta
	}
	if meta, ok := staticPageMeta[cleanPath]; ok {
		meta.Path = cleanPath
		return meta
	}
	if cleanPath == "/app" || strings.HasPrefix(cleanPath, "/app/") {
		return pageMeta{Title: "Workspace — m-stash", Description: seoDefaultDescription, Path: cleanPath, NoIndex: true}
	}

	segments := strings.Split(strings.Trim(cleanPath, "/"), "/")
	for index := range segments {
		segments[index] = normalizeResourcePathSegment(segments[index])
	}
	notFound := pageMeta{Title: "Page not found — m-stash", Description: seoDefaultDescription, Path: cleanPath, NoIndex: true}

	if segments[0] == "tutorials" {
		if len(segments) != 2 {
			return notFound
		}
		found, ok := findTutorial(segments[1])
		if !ok {
			return notFound
		}
		return pageMeta{Title: found.Title + " — m-stash tutorials", Description: found.Summary, Path: cleanPath, Type: "article"}
	}

	if segments[0] == "vibe" {
		// Share IDs are case-sensitive, so use the raw segment rather than the
		// lowercased one.
		shareID := strings.TrimPrefix(cleanPath, "/vibe/")
		if len(segments) != 2 || !validVibeShareID(shareID) {
			return notFound
		}
		// Shares expire within days, so they get rich link previews but stay out
		// of search indexes.
		meta := pageMeta{
			Title:       "A backend sketched with m-stash",
			Description: "See the data model, live demo, and copy-paste code m-stash generated for this app idea.",
			Path:        cleanPath,
			NoIndex:     true,
		}
		return app.enrichVibeShareMeta(ctx, meta, shareID)
	}

	if segments[0] == "public" {
		if len(segments) > 3 || validateResourceType(segments[1]) != nil {
			return notFound
		}
		if len(segments) == 2 {
			return pageMeta{
				Title:       segments[1] + " · public data — m-stash",
				Description: "Public " + segments[1] + " records served live by the m-stash API.",
				Path:        "/public/" + segments[1],
			}
		}
		if validateResourceSlug(segments[2]) != nil {
			return notFound
		}
		meta := pageMeta{
			Title:       segments[2] + " · " + segments[1] + " — m-stash",
			Description: "A public " + segments[1] + " record served live by the m-stash API.",
			Path:        "/public/" + segments[1] + "/" + segments[2],
			Type:        "article",
		}
		return app.enrichSharedResourceMeta(ctx, meta, segments[1], segments[2])
	}

	if len(segments) > 3 || validateNamespaceSlug(segments[0]) != nil {
		return notFound
	}
	username := segments[0]
	switch len(segments) {
	case 1:
		meta := pageMeta{
			Title:       "@" + username + " — m-stash",
			Description: "Public profile and records of @" + username + " on m-stash.",
			Path:        "/" + username,
			Type:        "profile",
		}
		return app.enrichProfileMeta(ctx, meta, username)
	case 2:
		if validateResourceType(segments[1]) != nil {
			return notFound
		}
		return pageMeta{
			Title:       segments[1] + " by @" + username + " — m-stash",
			Description: "Public " + segments[1] + " records shared by @" + username + " on m-stash.",
			Path:        "/" + username + "/" + segments[1],
		}
	default:
		if validateResourceType(segments[1]) != nil || validateResourceSlug(segments[2]) != nil {
			return notFound
		}
		meta := pageMeta{
			Title:       segments[2] + " · " + segments[1] + " by @" + username + " — m-stash",
			Description: "A public " + segments[1] + " record shared by @" + username + " on m-stash.",
			Path:        "/" + username + "/" + segments[1] + "/" + segments[2],
			Type:        "article",
		}
		return app.enrichUserResourceMeta(ctx, meta, username, segments[1], segments[2])
	}
}

// uiAppContext scopes lookups to the tenant the bundled UI is bound to.
func (app *application) uiAppContext(ctx context.Context) (context.Context, bson.ObjectID, bool) {
	if app.database == nil || app.uiAppKey == "" {
		return ctx, bson.ObjectID{}, false
	}
	record, err := app.lookupApp(ctx, app.uiAppKey)
	if err != nil {
		return ctx, bson.ObjectID{}, false
	}
	return withApp(ctx, record), record.ID, true
}

func (app *application) publicNamespaceBySlug(ctx context.Context, appID bson.ObjectID, username string) (Namespace, error) {
	var namespace Namespace
	err := app.database.Collection(namespaceCollectionName).FindOne(
		ctx,
		bson.M{"appId": appID, "slug": username, "isPublic": true},
	).Decode(&namespace)
	return namespace, err
}

func (app *application) publicResourceSummary(ctx context.Context, namespaceID bson.ObjectID, typeName, slug string) (Resource, error) {
	var resource Resource
	err := app.database.Collection(resourceCollectionName).FindOne(
		ctx,
		bson.M{"namespaceId": namespaceID, "type": typeName, "slug": slug, "visibility": publicVisibility},
		options.FindOne().SetProjection(bson.M{"title": 1, "summary": 1}),
	).Decode(&resource)
	return resource, err
}

func (app *application) enrichProfileMeta(ctx context.Context, meta pageMeta, username string) pageMeta {
	ctx, cancel := context.WithTimeout(ctx, seoLookupTimeout)
	defer cancel()
	ctx, appID, ok := app.uiAppContext(ctx)
	if !ok {
		return meta
	}
	namespace, err := app.publicNamespaceBySlug(ctx, appID, username)
	if err != nil {
		return markMissing(meta, err)
	}
	if name := strings.TrimSpace(namespace.DisplayName); name != "" && name != username {
		meta.Title = name + " (@" + username + ") — m-stash"
	}
	if bio := strings.TrimSpace(namespace.Bio); bio != "" {
		meta.Description = bio
	}
	return meta
}

func (app *application) enrichUserResourceMeta(ctx context.Context, meta pageMeta, username, typeName, slug string) pageMeta {
	ctx, cancel := context.WithTimeout(ctx, seoLookupTimeout)
	defer cancel()
	ctx, appID, ok := app.uiAppContext(ctx)
	if !ok {
		return meta
	}
	namespace, err := app.publicNamespaceBySlug(ctx, appID, username)
	if err != nil {
		return markMissing(meta, err)
	}
	resource, err := app.publicResourceSummary(ctx, namespace.ID, typeName, slug)
	if err != nil {
		return markMissing(meta, err)
	}
	return applyResourceMeta(meta, resource, " by @"+username+" — m-stash")
}

func (app *application) enrichSharedResourceMeta(ctx context.Context, meta pageMeta, typeName, slug string) pageMeta {
	ctx, cancel := context.WithTimeout(ctx, seoLookupTimeout)
	defer cancel()
	ctx, _, ok := app.uiAppContext(ctx)
	if !ok {
		return meta
	}
	namespace, err := app.sharedNamespace(ctx)
	if err != nil {
		return markMissing(meta, err)
	}
	resource, err := app.publicResourceSummary(ctx, namespace.ID, typeName, slug)
	if err != nil {
		return markMissing(meta, err)
	}
	return applyResourceMeta(meta, resource, " — m-stash")
}

func (app *application) enrichVibeShareMeta(ctx context.Context, meta pageMeta, shareID string) pageMeta {
	if app.database == nil {
		return meta
	}
	ctx, cancel := context.WithTimeout(ctx, seoLookupTimeout)
	defer cancel()
	record, err := app.loadVibeShare(ctx, shareID)
	if err != nil {
		return meta
	}
	if name := strings.TrimSpace(record.Spec.Name); name != "" {
		meta.Title = name + " — backend sketched with m-stash"
	}
	if tagline := strings.TrimSpace(record.Spec.Tagline); tagline != "" {
		meta.Description = tagline
	} else if idea := strings.TrimSpace(record.Idea); idea != "" {
		meta.Description = idea
	}
	return meta
}

func applyResourceMeta(meta pageMeta, resource Resource, titleSuffix string) pageMeta {
	if title := strings.TrimSpace(resource.Title); title != "" {
		meta.Title = title + titleSuffix
	}
	if summary := strings.TrimSpace(resource.Summary); summary != "" {
		meta.Description = summary
	}
	return meta
}

// markMissing keeps records that are private or deleted out of search indexes
// while tolerating transient database errors.
func markMissing(meta pageMeta, err error) pageMeta {
	if errors.Is(err, mongo.ErrNoDocuments) {
		meta.NoIndex = true
	}
	return meta
}

func truncateDescription(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= seoDescriptionLimit {
		return value
	}
	return strings.TrimSpace(string(runes[:seoDescriptionLimit-1])) + "…"
}

func renderSEOHead(baseURL, requestPath string, meta pageMeta) string {
	canonicalPath := meta.Path
	if canonicalPath == "" {
		canonicalPath = path.Clean("/" + requestPath)
	}
	canonicalURL := baseURL + canonicalPath
	imageURL := baseURL + "/og.png"
	ogType := meta.Type
	if ogType == "" {
		ogType = "website"
	}
	title := html.EscapeString(meta.Title)
	description := html.EscapeString(truncateDescription(meta.Description))

	var builder strings.Builder
	line := func(tag string) {
		builder.WriteString("    ")
		builder.WriteString(tag)
		builder.WriteString("\n")
	}
	builder.WriteString("    " + string(seoBlockStart) + "\n")
	line("<title>" + title + "</title>")
	line(`<meta name="description" content="` + description + `" />`)
	if meta.NoIndex {
		line(`<meta name="robots" content="noindex, nofollow" />`)
	} else {
		line(`<meta name="robots" content="index, follow, max-image-preview:large" />`)
		line(`<link rel="canonical" href="` + html.EscapeString(canonicalURL) + `" />`)
	}
	line(`<meta property="og:type" content="` + ogType + `" />`)
	line(`<meta property="og:site_name" content="` + seoSiteName + `" />`)
	line(`<meta property="og:locale" content="en_US" />`)
	line(`<meta property="og:title" content="` + title + `" />`)
	line(`<meta property="og:description" content="` + description + `" />`)
	line(`<meta property="og:url" content="` + html.EscapeString(canonicalURL) + `" />`)
	line(`<meta property="og:image" content="` + html.EscapeString(imageURL) + `" />`)
	line(`<meta property="og:image:width" content="1200" />`)
	line(`<meta property="og:image:height" content="630" />`)
	line(`<meta property="og:image:alt" content="` + seoImageAlt + `" />`)
	line(`<meta name="twitter:card" content="summary_large_image" />`)
	line(`<meta name="twitter:title" content="` + title + `" />`)
	line(`<meta name="twitter:description" content="` + description + `" />`)
	line(`<meta name="twitter:image" content="` + html.EscapeString(imageURL) + `" />`)
	line(`<meta name="twitter:image:alt" content="` + seoImageAlt + `" />`)
	if canonicalPath == "/discover" {
		line(`<script type="application/ld+json">` + siteStructuredData(baseURL) + `</script>`)
	}
	builder.WriteString("    " + string(seoBlockEnd))
	return builder.String()
}

func siteStructuredData(baseURL string) string {
	document := map[string]any{
		"@context": "https://schema.org",
		"@graph": []map[string]any{
			{
				"@type":       "WebSite",
				"@id":         baseURL + "/#website",
				"name":        seoSiteName,
				"url":         baseURL + "/discover",
				"description": seoDefaultDescription,
			},
			{
				"@type":               "SoftwareApplication",
				"name":                seoSiteName,
				"url":                 baseURL + "/discover",
				"image":               baseURL + "/og.png",
				"description":         seoDefaultDescription,
				"applicationCategory": "DeveloperApplication",
				"operatingSystem":     "Any",
				"offers":              map[string]any{"@type": "Offer", "price": "0", "priceCurrency": "USD"},
				"sameAs":              []string{"https://github.com/ranfysvalle02/m-stash"},
			},
		},
	}
	// json.Marshal escapes <, >, and & so the payload cannot close the script tag.
	encoded, _ := json.Marshal(document)
	return string(encoded)
}

// injectSEOHead swaps the default SEO block in index.html for route-specific tags.
func injectSEOHead(contents []byte, head string) []byte {
	start := bytes.Index(contents, seoBlockStart)
	end := bytes.Index(contents, seoBlockEnd)
	if start < 0 || end < start {
		return contents
	}
	lineStart := bytes.LastIndexByte(contents[:start], '\n') + 1
	result := make([]byte, 0, len(contents)+len(head))
	result = append(result, contents[:lineStart]...)
	result = append(result, head...)
	result = append(result, contents[end+len(seoBlockEnd):]...)
	return result
}

func (app *application) handleRobots(w http.ResponseWriter, r *http.Request) {
	body := "User-agent: *\nAllow: /\nDisallow: /app/\nDisallow: /console\n\nSitemap: " + app.publicBaseURL(r) + "/sitemap.xml\n"
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, "robots.txt", time.Time{}, strings.NewReader(body))
}

// handleSitemap lists the static pages plus public shared records. Personal
// public records are deliberately left out so users are not search-listed
// without opting in.
func (app *application) handleSitemap(w http.ResponseWriter, r *http.Request) {
	baseURL := app.publicBaseURL(r)
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	writeURL := func(location string, lastModified time.Time) {
		builder.WriteString("  <url><loc>" + html.EscapeString(baseURL+location) + "</loc>")
		if !lastModified.IsZero() {
			builder.WriteString("<lastmod>" + lastModified.UTC().Format("2006-01-02") + "</lastmod>")
		}
		builder.WriteString("</url>\n")
	}
	for _, staticPath := range sitemapStaticPaths {
		writeURL(staticPath, time.Time{})
	}
	if tutorials, err := loadTutorials(); err == nil {
		for _, entry := range tutorials {
			writeURL("/tutorials/"+entry.Slug, time.Time{})
		}
	}
	for _, resource := range app.publicSharedResourcesForSitemap(r.Context()) {
		writeURL("/public/"+resource.Type+"/"+resource.Slug, resource.UpdatedAt)
	}
	builder.WriteString("</urlset>\n")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	http.ServeContent(w, r, "sitemap.xml", time.Time{}, strings.NewReader(builder.String()))
}

func (app *application) publicSharedResourcesForSitemap(ctx context.Context) []Resource {
	ctx, cancel := context.WithTimeout(ctx, seoLookupTimeout)
	defer cancel()
	ctx, _, ok := app.uiAppContext(ctx)
	if !ok {
		return nil
	}
	namespace, err := app.sharedNamespace(ctx)
	if err != nil {
		return nil
	}
	cursor, err := app.database.Collection(resourceCollectionName).Find(
		ctx,
		bson.M{"namespaceId": namespace.ID, "visibility": publicVisibility},
		options.Find().
			SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
			SetLimit(seoSitemapLimit).
			SetProjection(bson.M{"type": 1, "slug": 1, "updatedAt": 1}),
	)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)
	var resources []Resource
	if err := cursor.All(ctx, &resources); err != nil {
		return nil
	}
	return resources
}

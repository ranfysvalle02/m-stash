package main

import (
	"fmt"
	"strconv"
	"strings"
)

// vibeSpec is the only thing the model (or a blueprint) can influence. It is a
// closed vocabulary: every free-text field is clamped and sanitized, and every
// other field is one of a fixed set. Nothing in it is ever executed or rendered
// as markup, so a hostile idea can shape copy and field names but never code.

const (
	// Audience maps a section onto m-stash's real ownership model.
	vibeAudienceOwner        = "owner"         // shared namespace, public read, owner (admin) writes
	vibeAudienceMember       = "member"        // personal namespace, private to each signed-in user
	vibeAudienceMemberPublic = "member-public" // personal namespace, public per-user pages

	vibeSpecMaxSections   = 4
	vibeSectionMaxFields  = 6
	vibeSectionMaxSamples = 4
	vibeNameMaxLength     = 40
	vibeTaglineMaxLength  = 120
	vibeLabelMaxLength    = 40
	vibeSampleTextMax     = 160
)

// Closed vocabularies. Anything outside these is defaulted, never passed through.
var (
	vibeAccents = map[string]bool{
		"teal": true, "indigo": true, "rose": true, "amber": true, "emerald": true, "slate": true,
	}
	vibeIcons = map[string]bool{
		"music": true, "cart": true, "book": true, "dumbbell": true, "note": true,
		"calendar": true, "trophy": true, "user": true, "box": true, "sparkles": true,
	}
	vibeViews = map[string]bool{
		"grid": true, "list": true, "checklist": true, "timeline": true, "ranked": true,
	}
	vibeFieldKinds = map[string]bool{
		"text": true, "longtext": true, "number": true, "price": true,
		"toggle": true, "date": true, "tag": true,
	}
	vibeAudiences = map[string]bool{
		vibeAudienceOwner: true, vibeAudienceMember: true, vibeAudienceMemberPublic: true,
	}
)

type vibeField struct {
	Key     string   `json:"key" bson:"key"`
	Label   string   `json:"label" bson:"label"`
	Kind    string   `json:"kind" bson:"kind"`
	Options []string `json:"options,omitempty" bson:"options,omitempty"`
}

type vibeSection struct {
	Type     string           `json:"type" bson:"type"`
	Title    string           `json:"title" bson:"title"`
	Audience string           `json:"audience" bson:"audience"`
	View     string           `json:"view" bson:"view"`
	Fields   []vibeField      `json:"fields" bson:"fields"`
	Samples  []map[string]any `json:"samples,omitempty" bson:"samples,omitempty"`
}

type vibeSpec struct {
	Name     string        `json:"name" bson:"name"`
	Tagline  string        `json:"tagline" bson:"tagline"`
	Accent   string        `json:"accent" bson:"accent"`
	Icon     string        `json:"icon" bson:"icon"`
	Sections []vibeSection `json:"sections" bson:"sections"`
	Source   string        `json:"source,omitempty" bson:"source,omitempty"`
}

type vibeSnippet struct {
	Title    string `json:"title" bson:"title"`
	Language string `json:"language" bson:"language"`
	Code     string `json:"code" bson:"code"`
}

// scopeAndVisibility translates an audience into the server's real ownership model.
func (section vibeSection) scope() string {
	if section.Audience == vibeAudienceOwner {
		return "shared"
	}
	return "personal"
}

func (section vibeSection) visibility() string {
	switch section.Audience {
	case vibeAudienceOwner, vibeAudienceMemberPublic:
		return publicVisibility
	default:
		return privateVisibility
	}
}

// sanitizeInline keeps letters, digits, and the small punctuation set an app name
// or label needs, dropping markup and control characters, then clamps the length.
// It mirrors sanitizeVibeIdea so copy can never carry hidden markup or instructions.
func sanitizeInline(value string, max int) string {
	var builder strings.Builder
	builder.Grow(len(value))
	lastSpace := false
	for _, character := range value {
		switch {
		case isInlineAllowed(character):
			builder.WriteRune(character)
			lastSpace = false
		case character == ' ' || character == '\t' || character == '\n' || character == '\r':
			if !lastSpace {
				builder.WriteByte(' ')
				lastSpace = true
			}
		default:
			// Drop control characters and structural markup (<, >, {, }, backticks, ...).
		}
	}
	return clampString(builder.String(), max)
}

func isInlineAllowed(character rune) bool {
	if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
		return true
	}
	return strings.ContainsRune(vibeIdeaAllowedPunctuation, character)
}

// normalizeVibeType turns a model-chosen bucket name into a slug the resource routes accept.
func normalizeVibeType(value string) string {
	var builder strings.Builder
	lastHyphen := true
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
			lastHyphen = false
		} else if !lastHyphen {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	return slug
}

// normalizeVibeFieldKey produces a short snake_case identifier for a data field.
func normalizeVibeFieldKey(value string) string {
	var builder strings.Builder
	lastUnderscore := true
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
			lastUnderscore = false
		} else if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	key := strings.Trim(builder.String(), "_")
	if len(key) > 32 {
		key = strings.Trim(key[:32], "_")
	}
	return key
}

func pick(value, fallback string, allowed map[string]bool) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if allowed[value] {
		return value
	}
	return fallback
}

// validateVibeSpec bounds and normalizes everything a model returned, dropping
// anything that would not survive the real API and defaulting the rest. It never
// errors: a hollow spec is still a usable (if minimal) app.
func validateVibeSpec(spec vibeSpec) vibeSpec {
	cleaned := vibeSpec{
		Name:    sanitizeInline(spec.Name, vibeNameMaxLength),
		Tagline: sanitizeInline(spec.Tagline, vibeTaglineMaxLength),
		Accent:  pick(spec.Accent, "teal", vibeAccents),
		Icon:    pick(spec.Icon, "box", vibeIcons),
		Source:  spec.Source,
	}
	if cleaned.Name == "" {
		cleaned.Name = "Your app"
	}

	seenScopeType := map[string]bool{}
	for _, section := range spec.Sections {
		if len(cleaned.Sections) >= vibeSpecMaxSections {
			break
		}
		cleanSection, ok := validateVibeSection(section, seenScopeType)
		if !ok {
			continue
		}
		cleaned.Sections = append(cleaned.Sections, cleanSection)
	}
	if len(cleaned.Sections) == 0 {
		cleaned.Sections = append(cleaned.Sections, defaultVibeSection())
	}
	return cleaned
}

func validateVibeSection(section vibeSection, seenScopeType map[string]bool) (vibeSection, bool) {
	typeName := normalizeVibeType(section.Type)
	if validateResourceType(typeName) != nil {
		return vibeSection{}, false
	}
	audience := pick(section.Audience, vibeAudienceMember, vibeAudiences)
	scope := "personal"
	if audience == vibeAudienceOwner {
		scope = "shared"
	}
	scopeType := scope + "/" + typeName
	if seenScopeType[scopeType] {
		return vibeSection{}, false
	}
	seenScopeType[scopeType] = true

	defaultView := "list"
	if audience == vibeAudienceOwner {
		defaultView = "grid"
	}
	cleaned := vibeSection{
		Type:     typeName,
		Title:    sanitizeInline(section.Title, vibeLabelMaxLength),
		Audience: audience,
		View:     pick(section.View, defaultView, vibeViews),
	}
	if cleaned.Title == "" {
		cleaned.Title = strings.Title(strings.ReplaceAll(typeName, "-", " ")) //nolint:staticcheck // ASCII titles only
	}

	seenKey := map[string]bool{}
	for _, field := range section.Fields {
		if len(cleaned.Fields) >= vibeSectionMaxFields {
			break
		}
		key := normalizeVibeFieldKey(field.Key)
		if key == "" || seenKey[key] {
			continue
		}
		seenKey[key] = true
		cleanField := vibeField{
			Key:   key,
			Label: sanitizeInline(field.Label, vibeLabelMaxLength),
			Kind:  pick(field.Kind, "text", vibeFieldKinds),
		}
		if cleanField.Label == "" {
			cleanField.Label = strings.Title(strings.ReplaceAll(key, "_", " ")) //nolint:staticcheck // ASCII labels only
		}
		for _, option := range field.Options {
			if len(cleanField.Options) >= 8 {
				break
			}
			if cleanOption := sanitizeInline(option, 24); cleanOption != "" {
				cleanField.Options = append(cleanField.Options, cleanOption)
			}
		}
		cleaned.Fields = append(cleaned.Fields, cleanField)
	}
	if len(cleaned.Fields) == 0 {
		cleaned.Fields = []vibeField{{Key: "title", Label: "Title", Kind: "text"}}
	}

	// Samples only make sense for owner sections, which the sandbox seeds publicly.
	if audience == vibeAudienceOwner {
		for _, sample := range section.Samples {
			if len(cleaned.Samples) >= vibeSectionMaxSamples {
				break
			}
			if coerced := coerceSample(cleaned.Fields, sample); len(coerced) > 0 {
				cleaned.Samples = append(cleaned.Samples, coerced)
			}
		}
	}
	return cleaned, true
}

func defaultVibeSection() vibeSection {
	return vibeSection{
		Type:     "item",
		Title:    "Items",
		Audience: vibeAudienceMember,
		View:     "list",
		Fields: []vibeField{
			{Key: "title", Label: "Title", Kind: "text"},
			{Key: "notes", Label: "Notes", Kind: "longtext"},
		},
	}
}

// coerceSample keeps only known field keys and coerces each value to its field kind.
func coerceSample(fields []vibeField, sample map[string]any) map[string]any {
	out := map[string]any{}
	for _, field := range fields {
		raw, ok := sample[field.Key]
		if !ok {
			continue
		}
		out[field.Key] = coerceSampleValue(field.Kind, raw)
	}
	return out
}

func coerceSampleValue(kind string, raw any) any {
	switch kind {
	case "number", "price":
		switch value := raw.(type) {
		case float64:
			return value
		case int:
			return float64(value)
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				return parsed
			}
		}
		return float64(0)
	case "toggle":
		switch value := raw.(type) {
		case bool:
			return value
		case string:
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
		return false
	case "tag":
		switch value := raw.(type) {
		case []any:
			tags := []string{}
			for _, item := range value {
				if text, ok := item.(string); ok {
					if clean := sanitizeInline(text, 24); clean != "" {
						tags = append(tags, clean)
					}
				}
				if len(tags) >= 6 {
					break
				}
			}
			return tags
		case []string:
			tags := []string{}
			for _, item := range value {
				if clean := sanitizeInline(item, 24); clean != "" {
					tags = append(tags, clean)
				}
				if len(tags) >= 6 {
					break
				}
			}
			return tags
		case string:
			return sanitizeInline(value, vibeSampleTextMax)
		}
		return ""
	default: // text, longtext, date
		if text, ok := raw.(string); ok {
			return sanitizeInline(text, vibeSampleTextMax)
		}
		return fmt.Sprint(raw)
	}
}

// sampleTitle derives a human title for a seeded sample from its first text field.
func sampleTitle(section vibeSection, sample map[string]any) string {
	for _, field := range section.Fields {
		if field.Kind != "text" && field.Kind != "longtext" {
			continue
		}
		if value, ok := sample[field.Key].(string); ok && strings.TrimSpace(value) != "" {
			return clampString(value, 120)
		}
	}
	return strings.Title(strings.ReplaceAll(section.Type, "-", " ")) //nolint:staticcheck // ASCII titles only
}

// sampleSummary derives an optional summary from the first longtext field.
func sampleSummary(section vibeSection, sample map[string]any) string {
	for _, field := range section.Fields {
		if field.Kind != "longtext" {
			continue
		}
		if value, ok := sample[field.Key].(string); ok {
			return clampString(value, 500)
		}
	}
	return ""
}

func (spec vibeSpec) ownerSections() []vibeSection {
	sections := []vibeSection{}
	for _, section := range spec.Sections {
		if section.Audience == vibeAudienceOwner {
			sections = append(sections, section)
		}
	}
	return sections
}

func (spec vibeSpec) memberSections() []vibeSection {
	sections := []vibeSection{}
	for _, section := range spec.Sections {
		if section.Audience != vibeAudienceOwner {
			sections = append(sections, section)
		}
	}
	return sections
}

// renderVibeSnippets builds copy-paste fetch() calls from the spec and the live
// base URL + key. They are generated per request and never stored, so a share can
// never carry a snippet pointed at an attacker-chosen host.
func renderVibeSnippets(spec vibeSpec, baseURL, appKey string) []vibeSnippet {
	if appKey == "" {
		appKey = vibeStarterPlaceholder
	}
	preamble := "const API = " + strconv.Quote(baseURL) + ";\n" +
		"const APP = " + strconv.Quote(appKey) + "; // publishable key — safe in frontend code\n\n"

	snippets := []vibeSnippet{}

	if owner := spec.ownerSections(); len(owner) > 0 {
		section := owner[0]
		snippets = append(snippets, vibeSnippet{
			Title:    "Read the public " + strings.ToLower(section.Title),
			Language: "javascript",
			Code: preamble + "const res = await fetch(`${API}/v1/public/shared/resources/" + section.Type + "`, {\n" +
				"  headers: { \"X-M-Stash-App\": APP },\n" +
				"});\n" +
				"const { data } = await res.json(); // every client reads these, no login",
		})
	}

	if member := spec.memberSections(); len(member) > 0 {
		section := member[0]
		body := vibeSampleBody(section)
		snippets = append(snippets, vibeSnippet{
			Title:    "Sign up a customer and save a " + strings.ToLower(singular(section.Title)),
			Language: "javascript",
			Code: preamble + "// 1. Your customer signs up in your UI\n" +
				"const signup = await fetch(`${API}/v1/auth/signup`, {\n" +
				"  method: \"POST\",\n" +
				"  headers: { \"X-M-Stash-App\": APP, \"Content-Type\": \"application/json\" },\n" +
				"  body: JSON.stringify({ email, password, username }),\n" +
				"});\n" +
				"const { token } = await signup.json();\n\n" +
				"// 2. Save a private record the server scopes to them\n" +
				"await fetch(`${API}/v1/me/resources/" + section.Type + "`, {\n" +
				"  method: \"POST\",\n" +
				"  headers: {\n" +
				"    \"X-M-Stash-App\": APP,\n" +
				"    \"Authorization\": `Bearer ${token}`,\n" +
				"    \"Content-Type\": \"application/json\",\n" +
				"  },\n" +
				"  body: JSON.stringify(" + body + "),\n" +
				"});",
		})
	}

	if owner := spec.ownerSections(); len(owner) > 0 {
		section := owner[0]
		body := vibeSampleBody(section)
		snippets = append(snippets, vibeSnippet{
			Title:    "Owner publishes a " + strings.ToLower(singular(section.Title)),
			Language: "javascript",
			Code: preamble + "// The owner logs in (admin), then writes app-wide data\n" +
				"const login = await fetch(`${API}/v1/auth/login`, {\n" +
				"  method: \"POST\",\n" +
				"  headers: { \"X-M-Stash-App\": APP, \"Content-Type\": \"application/json\" },\n" +
				"  body: JSON.stringify({ email, password }),\n" +
				"});\n" +
				"const { token } = await login.json();\n\n" +
				"await fetch(`${API}/v1/shared/resources/" + section.Type + "`, {\n" +
				"  method: \"POST\",\n" +
				"  headers: {\n" +
				"    \"X-M-Stash-App\": APP,\n" +
				"    \"Authorization\": `Bearer ${token}`,\n" +
				"    \"Content-Type\": \"application/json\",\n" +
				"  },\n" +
				"  body: JSON.stringify(" + body + "),\n" +
				"}); // in production your backend uses the app secret key instead",
		})
	}

	return snippets
}

// vibeSampleBody renders a representative request body literal for a section.
func vibeSampleBody(section vibeSection) string {
	data := map[string]any{}
	for _, field := range section.Fields {
		data[field.Key] = exampleForKind(field)
	}
	return "{\n    slug: \"" + section.Type + "-1\",\n    title: \"My " + strings.ToLower(singular(section.Title)) + "\",\n    visibility: \"" + section.visibility() + "\",\n    data: " + inlineJSON(section.Fields, data) + ",\n  }"
}

func inlineJSON(fields []vibeField, data map[string]any) string {
	parts := []string{}
	for _, field := range fields {
		parts = append(parts, strconv.Quote(field.Key)+": "+exampleLiteral(field, data[field.Key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func exampleForKind(field vibeField) any {
	switch field.Kind {
	case "number":
		return 1
	case "price":
		return 19.99
	case "toggle":
		return false
	case "date":
		return "2025-01-01"
	case "tag":
		return []string{"example"}
	default:
		return strings.ToLower(field.Label)
	}
}

func exampleLiteral(field vibeField, value any) string {
	switch field.Kind {
	case "number":
		return "1"
	case "price":
		return "19.99"
	case "toggle":
		return "false"
	case "tag":
		return `["example"]`
	default:
		if text, ok := value.(string); ok {
			return strconv.Quote(text)
		}
		return strconv.Quote(fmt.Sprint(value))
	}
}

func singular(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasSuffix(title, "ies") {
		return title[:len(title)-3] + "y"
	}
	if strings.HasSuffix(title, "s") && !strings.HasSuffix(title, "ss") {
		return title[:len(title)-1]
	}
	return title
}

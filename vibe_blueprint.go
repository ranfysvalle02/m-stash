package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type vibeBlueprintRule struct {
	keywords []string
	part     vibeModelPart
	slug     string
	data     map[string]any
}

// vibeBlueprintRules are checked in order; each matching rule contributes one data-model piece.
var vibeBlueprintRules = []vibeBlueprintRule{
	{
		keywords: []string{"game", "save", "slot", "level", "progress", "player"},
		part:     vibeModelPart{Name: "Save slot", Scope: "personal", Type: "save", Visibility: "private", Purpose: "Each player's progress, one record per slot. Only the owner can read or overwrite it."},
		slug:     "slot-1",
		data:     map[string]any{"level": 1, "coins": 0},
	},
	{
		keywords: []string{"leaderboard", "score", "rank", "high score", "standings", "competition"},
		part:     vibeModelPart{Name: "Leaderboard", Scope: "shared", Type: "leaderboard", Visibility: "public", Purpose: "App-wide standings. Your backend writes it with the secret key; anyone can read it."},
		slug:     "global",
		data:     map[string]any{"entries": []any{}},
	},
	{
		keywords: []string{"habit", "streak", "routine", "workout", "goal", "fitness"},
		part:     vibeModelPart{Name: "Habit", Scope: "personal", Type: "habit", Visibility: "private", Purpose: "A user's habit and its streak count. Bump the streak atomically with PATCH."},
		slug:     "daily-walk",
		data:     map[string]any{"streak": 0},
	},
	{
		keywords: []string{"recipe", "cook", "meal", "ingredient", "kitchen"},
		part:     vibeModelPart{Name: "Recipe", Scope: "personal", Type: "recipe", Visibility: "private", Purpose: "A user's recipe. Private by default; set visibility to public to publish it."},
		slug:     "tomato-soup",
		data:     map[string]any{"ingredients": []any{"tomatoes", "basil"}, "minutes": 30},
	},
	{
		keywords: []string{"note", "journal", "diary", "writing", "memo"},
		part:     vibeModelPart{Name: "Note", Scope: "personal", Type: "note", Visibility: "private", Purpose: "A user's private note. Nobody else, not even other signed-in users, can read it."},
		slug:     "first-note",
		data:     map[string]any{"body": "Hello from m-stash"},
	},
	{
		keywords: []string{"todo", "to-do", "task", "checklist", "project"},
		part:     vibeModelPart{Name: "Task", Scope: "personal", Type: "task", Visibility: "private", Purpose: "A user's task with its done state."},
		slug:     "first-task",
		data:     map[string]any{"done": false},
	},
	{
		keywords: []string{"bookmark", "link", "reel", "video", "playlist", "collection", "gallery", "photo"},
		part:     vibeModelPart{Name: "Collection", Scope: "personal", Type: "collection", Visibility: "private", Purpose: "A user's list of saved items. Publish a collection by setting visibility to public."},
		slug:     "favorites",
		data:     map[string]any{"items": []any{}},
	},
	{
		keywords: []string{"setting", "preference", "theme", "dark mode"},
		part:     vibeModelPart{Name: "Preferences", Scope: "personal", Type: "preferences", Visibility: "private", Purpose: "A user's settings, loaded on sign-in."},
		slug:     "main",
		data:     map[string]any{"theme": "dark"},
	},
	{
		keywords: []string{"config", "feature flag", "announcement", "catalog", "menu", "pricing"},
		part:     vibeModelPart{Name: "App config", Scope: "shared", Type: "config", Visibility: "public", Purpose: "App-wide settings every client reads. Only an admin or your backend can change it."},
		slug:     "main",
		data:     map[string]any{"version": "1.0.0"},
	},
	{
		keywords: []string{"profile", "public page", "portfolio", "publish", "share", "social"},
		part:     vibeModelPart{Name: "Public profile", Scope: "personal", Type: "profile", Visibility: "public", Purpose: "The face a user shows the world, readable by anyone at /v1/public/{username}/profile/main."},
		slug:     "main",
		data:     map[string]any{"bio": "Hi there"},
	},
}

var vibeBlueprintDefaults = []vibeBlueprintRule{
	{
		part: vibeModelPart{Name: "Item", Scope: "personal", Type: "item", Visibility: "private", Purpose: "The core thing each user saves. Only its owner can read or change it."},
		slug: "first-item",
		data: map[string]any{"value": "anything JSON"},
	},
	vibeBlueprintRules[len(vibeBlueprintRules)-1],
}

const vibeBlueprintMaxParts = 4

// buildVibeBlueprint sketches a plan without any model call, so the lab answers even when the upstream is down.
func buildVibeBlueprint(idea, baseURL string) *vibePlan {
	lowered := strings.ToLower(idea)
	var matched []vibeBlueprintRule
	for _, rule := range vibeBlueprintRules {
		for _, keyword := range rule.keywords {
			if strings.Contains(lowered, keyword) {
				matched = append(matched, rule)
				break
			}
		}
		if len(matched) == vibeBlueprintMaxParts {
			break
		}
	}
	if len(matched) == 0 {
		matched = vibeBlueprintDefaults
	}

	plan := &vibePlan{Source: vibeSourceBlueprint}
	var personal, shared []string
	for _, rule := range matched {
		plan.DataModel = append(plan.DataModel, rule.part)
		label := fmt.Sprintf("%s (%s)", rule.part.Type, rule.part.Visibility)
		if rule.part.Scope == "shared" {
			shared = append(shared, label)
		} else {
			personal = append(personal, label)
		}
	}

	summary := fmt.Sprintf("A backend for %q: users sign up in your app, and m-stash stores", clampString(idea, 120))
	if len(personal) > 0 {
		summary += " per-user records (" + strings.Join(personal, ", ") + ") that only their owner can change"
	}
	if len(shared) > 0 {
		if len(personal) > 0 {
			summary += ", plus"
		}
		summary += " app-wide records (" + strings.Join(shared, ", ") + ") that only your backend can write"
	}
	plan.Summary = summary + "."

	plan.Snippets = append(plan.Snippets, vibeSnippet{
		Title:    "Sign up a user and keep the token",
		Language: "javascript",
		Code: vibeSnippetPreamble(baseURL) + `const res = await fetch(` + "`${API}/v1/auth/signup`" + `, {
  method: "POST",
  headers: { "X-M-Stash-App": APP, "Content-Type": "application/json" },
  body: JSON.stringify({ email, password, username }),
});
const { token } = await res.json(); // use /v1/auth/login next time`,
	})

	if rule, ok := firstRuleWithScope(matched, "personal"); ok {
		body, _ := json.Marshal(map[string]any{
			"slug":       rule.slug,
			"title":      rule.part.Name,
			"visibility": rule.part.Visibility,
			"data":       rule.data,
		})
		plan.Snippets = append(plan.Snippets, vibeSnippet{
			Title:    "Save a " + strings.ToLower(rule.part.Name) + " for the signed-in user",
			Language: "javascript",
			Code: `await fetch(` + "`${API}/v1/me/resources/" + rule.part.Type + "`" + `, {
  method: "POST",
  headers: {
    "X-M-Stash-App": APP,
    "Authorization": ` + "`Bearer ${token}`" + `,
    "Content-Type": "application/json",
  },
  body: JSON.stringify(` + string(body) + `),
});
// Read it back: GET ` + "`${API}/v1/me/resources/" + rule.part.Type + "/" + rule.slug + "`",
		})
	}

	if rule, ok := firstPublicRule(matched); ok {
		path := "`${API}/v1/public/${username}/" + rule.part.Type + "/" + rule.slug + "`"
		title := "Read a public " + strings.ToLower(rule.part.Name) + " with no login"
		if rule.part.Scope == "shared" {
			path = "`${API}/v1/public/shared/resources/" + rule.part.Type + "/" + rule.slug + "`"
		}
		plan.Snippets = append(plan.Snippets, vibeSnippet{
			Title:    title,
			Language: "javascript",
			Code: `const res = await fetch(` + path + `, {
  headers: { "X-M-Stash-App": APP },
});
const { data } = await res.json();`,
		})
	}

	plan.WhyMStash = "Ownership comes from the signed-in identity on the server, so one user can never read or overwrite another's records, and MongoDB never touches the browser."
	return plan
}

func vibeSnippetPreamble(baseURL string) string {
	return `const API = "` + baseURL + `";
const APP = "pk_your_publishable_key"; // safe in frontend code
`
}

func firstRuleWithScope(rules []vibeBlueprintRule, scope string) (vibeBlueprintRule, bool) {
	for _, rule := range rules {
		if rule.part.Scope == scope {
			return rule, true
		}
	}
	return vibeBlueprintRule{}, false
}

func firstPublicRule(rules []vibeBlueprintRule) (vibeBlueprintRule, bool) {
	for _, rule := range rules {
		if rule.part.Visibility == "public" {
			return rule, true
		}
	}
	return vibeBlueprintRule{}, false
}

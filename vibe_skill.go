package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Agent skills are plug-and-play SKILL.md files for coding agents (Cursor,
// Claude Code, Codex, ...). Two flavors are served:
//   - the generic m-stash skill at GET /skill.md, stamped with this deployment's
//     origin, teaching any agent the whole API contract; and
//   - a spec-tailored skill at GET /v1/vibe/{id}/skill.md (and bundled flat into
//     the starter .zip) pre-filled with one app's publishable key and sections.
//
// Both carry valid YAML frontmatter (name + description) so an agent can
// discover and auto-apply them, and never embed a secret key, password, or
// claim token.

// handleSkill serves the generic, deployment-stamped m-stash agent skill. It is
// always available (like /llms.txt) and reveals no app-specific data.
func (app *application) handleSkill(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	skill := genericVibeSkill(app.publicBaseURL(r), app.effectiveSettings(r.Context()).TrySandbox)
	writeSkill(w, r, skill)
}

// writeSkill writes a SKILL.md body as markdown, optionally as a download so a
// one-click "Download SKILL.md" lands a real file instead of rendering in-tab.
func writeSkill(w http.ResponseWriter, r *http.Request, skill string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="SKILL.md"`)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(skill))
}

// skillSlugify reduces a name to lowercase letters, digits, and single hyphens.
func skillSlugify(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, character := range strings.ToLower(value) {
		switch {
		case (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9'):
			b.WriteRune(character)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// skillNameFromSpec builds a valid skill name (lowercase/hyphen, <= 64 chars)
// from the app's spec name, always prefixed with "m-stash-".
func skillNameFromSpec(spec vibeSpec) string {
	slug := skillSlugify(spec.Name)
	if slug == "" {
		slug = "app"
	}
	name := "m-stash-" + slug
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-")
	}
	return name
}

// yamlDoubleQuoted wraps a value as a YAML double-quoted scalar so punctuation
// such as ':' in a description can never break the frontmatter.
func yamlDoubleQuoted(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

// genericVibeSkill is the deployment-wide m-stash skill. It needs no app context
// and stamps in the live origin so an agent can call the API immediately.
func genericVibeSkill(baseURL string, trySandbox bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: m-stash\n")
	b.WriteString("description: " + yamlDoubleQuoted("Integrate m-stash for authentication and ownership-scoped data (per-user and app-wide) over a small REST API backed by MongoDB. Use when wiring sign-up, login, sessions, private per-user records, or shared app data, or when the user mentions m-stash, the X-M-Stash-App header, a publishable key (pk_), or /v1/me resources.") + "\n")
	b.WriteString("---\n\n")

	b.WriteString("# m-stash\n\n")
	b.WriteString("m-stash is a small REST API that gives an app authentication plus per-user and app-wide data on MongoDB, with record ownership enforced on the server. Use it to add sign-up, login, and private per-user data without wiring a database or writing security rules.\n\n")

	b.WriteString("## Connect\n\n")
	fmt.Fprintf(&b, "- API base: `%s`\n", baseURL)
	b.WriteString("- Identify the app on EVERY request with the header `X-M-Stash-App: <publishableKey>` (a `pk_...` key, safe in frontend code). Browsers that drop custom headers on preflight can use `?app=<publishableKey>` instead.\n")
	b.WriteString("- Call `fetch(url, { credentials: \"omit\" })`; m-stash never relies on cookies from a third-party frontend.\n")
	b.WriteString("- After signup/login keep the returned bearer `token`; send `Authorization: Bearer <token>` on authenticated calls.\n\n")

	b.WriteString("## Endpoints\n\n")
	b.WriteString("- `POST /v1/auth/signup` `{ email, password, username }` -> `{ token, user, namespace }`. Your users sign up in your app.\n")
	b.WriteString("- `POST /v1/auth/login` `{ email, password }` -> `{ token, user }`. The app owner (admin) signs in here too.\n")
	b.WriteString("- Social login (only if `GET /v1/auth/providers` lists a provider): full-page navigate to `/v1/auth/oauth/{provider}/start?app=<key>&redirect_uri=<yourUrl>`; the user returns to `redirect_uri?oauth_code=otc_...`; within 60s call `POST /v1/auth/oauth/exchange` `{ code }` (with the `X-M-Stash-App` header) -> `{ token, user, namespace, isNewUser }`. Never place the JWT in a URL; the `otc_` code is single-use.\n")
	b.WriteString("- `GET|POST /v1/me/resources/{type}` and `GET|PUT|PATCH|DELETE /v1/me/resources/{type}/{slug}` — the signed-in user's PRIVATE records. `PATCH` atomically increments a numeric `data.*` field.\n")
	b.WriteString("- `GET|POST /v1/shared/resources/{type}` and `/{type}/{slug}` — app-wide records. Reads need any signed-in user; writes need an admin session or the app secret key (`sk_`, server only).\n")
	b.WriteString("- `GET /v1/public/shared/resources/{type}[/{slug}]` — public app-wide records, no login.\n")
	b.WriteString("- `GET /v1/public/{username}/{type}[/{slug}]` — a user's public records on their own page.\n\n")

	b.WriteString("## A record body\n\n")
	b.WriteString("```json\n{\n  \"slug\": \"unique-slug\",\n  \"title\": \"Human title\",\n  \"visibility\": \"private\",\n  \"data\": { \"fieldKey\": \"value\" }\n}\n```\n\n")
	writeSkillRecordRules(&b)

	if trySandbox {
		b.WriteString("## Zero-setup sandbox\n\n")
		b.WriteString("No key yet? Provision a real, throwaway app with one call — no account needed:\n\n")
		fmt.Fprintf(&b, "```bash\ncurl -X POST \"%s/v1/try\"\n# -> { \"publishableKey\": \"pk_...\", \"expiresAt\": \"...\", \"apiBase\": \"...\", \"claim\": { \"token\": \"...\" } }\n```\n\n", baseURL)
		b.WriteString("Use the returned `publishableKey` as `X-M-Stash-App`. The sandbox is a real isolated app that expires unless claimed; keep the one-time `claim.token` only if you intend to keep it.\n\n")
	}

	b.WriteString("## Rules (do not break)\n\n")
	b.WriteString("- Always send `X-M-Stash-App: <publishableKey>` on every request.\n")
	b.WriteString("- NEVER put a secret key (`sk_...`), platform token, or MongoDB credentials in browser/frontend code — they are server-only.\n")
	b.WriteString("- The browser never chooses an owner; the server scopes every record to the token's user. Don't work around a 401/403 on the client — fix the auth or the visibility.\n")
	b.WriteString("- Default new records to `private`; choose `public` only for data meant for everyone.\n\n")

	writeSkillFailures(&b, true)

	fmt.Fprintf(&b, "## Reference\n\nFull machine-readable API reference: %s/llms.txt\n", baseURL)
	return b.String()
}

// vibeAppSkill is the spec-tailored skill for one generated app. It embeds the
// app's publishable key and exact sections, but never samples, passwords, or
// claim tokens.
func vibeAppSkill(record *vibeShareRecord, baseURL string) string {
	spec := record.Spec
	appName := spec.Name
	if appName == "" {
		appName = "this app"
	}
	publishableKey := record.PublishableKey
	if publishableKey == "" {
		publishableKey = vibeStarterPlaceholder
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", skillNameFromSpec(spec))
	description := "Build and extend " + appName + ", an app backed by m-stash for authentication and ownership-scoped data. Use when editing this app or wiring its sign-up, login, public app data, or private per-user records against the m-stash API."
	b.WriteString("description: " + yamlDoubleQuoted(clampString(description, 480)) + "\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", appName)
	b.WriteString("A vanilla HTML/CSS/JS app backed by **m-stash** (authentication plus per-user and app-wide data, with ownership enforced on the server). Follow these rules so changes stay safe and keep working.\n\n")

	b.WriteString("## Connect\n\n")
	fmt.Fprintf(&b, "- API base: `%s`\n", baseURL)
	fmt.Fprintf(&b, "- Publishable key (frontend-safe, names the app): `%s`\n", publishableKey)
	b.WriteString("- Every request: send header `X-M-Stash-App: <key>` (or `?app=<key>`), and call `fetch(url, { credentials: \"omit\" })`.\n")
	b.WriteString("- After signup/login keep the bearer `token`; send `Authorization: Bearer <token>` on authenticated calls.\n\n")

	b.WriteString("## This app's data\n\n")
	for _, section := range spec.Sections {
		title := section.Title
		if title == "" {
			title = section.Type
		}
		scope := "/v1/me/resources/"
		who := "the signed-in user (private to them)"
		if section.Audience == vibeAudienceOwner {
			scope = "/v1/shared/resources/"
			who = "the app owner (everyone can read)"
		}
		fmt.Fprintf(&b, "### %s — `%s%s`\n\n", title, scope, section.Type)
		fmt.Fprintf(&b, "- Written by %s; visibility `%s`.\n", who, section.visibility())
		for _, field := range section.Fields {
			line := "- `" + field.Key + "` (" + field.Kind + ")"
			if len(field.Options) > 0 {
				line += " — one of: " + strings.Join(field.Options, ", ")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## A record body\n\n")
	b.WriteString("```json\n{\n  \"slug\": \"unique-slug\",\n  \"title\": \"Human title\",\n  \"visibility\": \"private\",\n  \"data\": { \"fieldKey\": \"value\" }\n}\n```\n\n")
	writeSkillRecordRules(&b)
	writeAppSkillProcedures(&b)
	writeAppSkillVerify(&b, spec, baseURL, publishableKey)
	writeSkillFailures(&b, false)

	b.WriteString("## Rules (do not break)\n\n")
	b.WriteString("- Always send `X-M-Stash-App` with the publishable key above.\n")
	b.WriteString("- NEVER put a secret key (`sk_...`) or platform token in frontend/source files — those are server-only.\n")
	b.WriteString("- Ownership and visibility are enforced by the server; don't work around a 401/403 on the client — fix the auth or the visibility instead.\n\n")

	fmt.Fprintf(&b, "Full machine-readable reference: %s/llms.txt\n", baseURL)
	return b.String()
}

// writeSkillRecordRules states the record contract exactly as the server
// enforces it, so an agent's first write succeeds.
func writeSkillRecordRules(b *strings.Builder) {
	b.WriteString("Every create needs `slug` (3-64 lowercase letters, digits, or hyphens; unique per type), `title` (1-200 chars), and `visibility`; `data` is free-form JSON. `visibility` is REQUIRED and must be `private` (owner only), `authenticated` (any signed-in user; shared resources only), or `public` (anyone).\n\n")
}

// writeSkillFailures maps the API's real failure responses to their fixes.
// Agents hit these constantly; a lookup table beats guessing. The app-tailored
// skill omits the credential row so it never carries credential wording.
func writeSkillFailures(b *strings.Builder, includeCredentials bool) {
	b.WriteString("## When a call fails\n\n")
	b.WriteString("| Status | Message contains | Fix |\n| --- | --- | --- |\n")
	b.WriteString("| 400 | `Missing X-M-Stash-App` | Send the `X-M-Stash-App` header (or `?app=`) on every request. |\n")
	b.WriteString("| 400 | `visibility must be` | Send `visibility`: `private`, `public`, or (shared only) `authenticated`. |\n")
	b.WriteString("| 400 | `title must be` / `resource slug must` | Send a non-empty `title` and a 3-64 char lowercase-hyphen `slug`. |\n")
	if includeCredentials {
		b.WriteString("| 400 | `password must be` | Passwords are 12-72 characters. |\n")
	}
	b.WriteString("| 401 | `Unknown application key` | Wrong `pk_` key, or the sandbox expired. Keep it from the console before it expires, or provision a new one. |\n")
	b.WriteString("| 401 | `Missing or invalid Authorization` | Send `Authorization: Bearer <token>` from signup/login. |\n")
	b.WriteString("| 403 | `Invalid or expired token` | Sign in again and replace the stored token. |\n")
	b.WriteString("| 403 | `Only administrators can write shared` | Members write to `/v1/me/resources/...`; shared data is written by the owner (admin login) or a server holding `sk_`. |\n")
	b.WriteString("| 409 | `already exists` | Pick a different slug, email, or username. |\n")
	b.WriteString("| 413 / 429 | `too large` / `limit` / `full` | A quota was reached: smaller records, fewer records, or keep the app from the console for higher limits. |\n\n")
}

// writeAppSkillProcedures is the "how to change this app" playbook. The
// starter renders entirely from its config, so most features are a data edit.
func writeAppSkillProcedures(b *strings.Builder) {
	b.WriteString("## Change the app\n\n")
	b.WriteString("The starter renders entirely from the JSON in the `<script id=\"m-stash-config\">` block of `index.html` (`sections` -> `fields`). `mstash.json` is the full spec, with seed samples, for reference; keep the two in sync. Most features are a config edit, not new code:\n\n")
	b.WriteString("1. **Add a field**: append `{ \"key\": \"snake_case\", \"label\": \"Label\", \"kind\": \"text\" }` to a section's `fields`. Kinds: `text`, `longtext`, `number`, `price`, `toggle`, `date`, `tag`. Add `\"options\": [\"a\", \"b\"]` for a dropdown. `data` is free-form, so there is no migration; older records just lack the key.\n")
	b.WriteString("2. **Add a section**: give it a new `type` (3-32 lowercase letters, digits, or single hyphens; it becomes the URL segment), an `audience` (`owner` = app-wide data the owner writes and everyone reads; `member` = private per user; `member-public` = per user, public on their page), a `view` (`grid`, `list`, `checklist`, `timeline`, `ranked`), and fields. No server setup is needed.\n")
	b.WriteString("3. **Counters** (likes, votes, stock): `PATCH` the record with `{ \"op\": \"increment\", \"field\": \"data.likes\", \"by\": 1 }`; it is atomic, so never read-modify-write.\n")
	b.WriteString("4. **Never rename** an existing field `key` or section `type` that already holds records; add a new one instead. Labels and titles are safe to change.\n")
	b.WriteString("5. **Custom UI** beyond the built-in views: keep calling the same endpoints with `fetch`; the server enforces ownership whatever the frontend does.\n\n")
}

// writeAppSkillVerify gives a concrete, copy-paste check for this app's own
// sections so an agent can prove a change works instead of assuming it does.
func writeAppSkillVerify(b *strings.Builder, spec vibeSpec, baseURL, publishableKey string) {
	b.WriteString("## Verify a change\n\n")
	b.WriteString("Run this after any change (it creates one throwaway user; use a sandbox or dev app). Every call must return 2xx; `curl -f` exits non-zero otherwise.\n\n")
	b.WriteString("```bash\n")
	fmt.Fprintf(b, "API=%s\nKEY=%s\n", shellQuote(baseURL), shellQuote(publishableKey))
	for _, section := range spec.ownerSections() {
		fmt.Fprintf(b, "# Public %s are readable without login\n", section.Type)
		fmt.Fprintf(b, `curl -fsS "$API/v1/public/shared/resources/%s" -H "X-M-Stash-App: $KEY"`+"\n", section.Type)
	}
	if members := spec.memberSections(); len(members) > 0 {
		b.WriteString("# A new member can sign up, then write and read their own records\n")
		b.WriteString("SUFFIX=$RANDOM$RANDOM\n")
		b.WriteString(`TOKEN=$(curl -fsS -X POST "$API/v1/auth/signup" -H "X-M-Stash-App: $KEY" -H 'Content-Type: application/json' \` + "\n")
		b.WriteString(`  -d '{"email":"verify-'"$SUFFIX"'@example.com","password":"verify-password-123","username":"verify-'"$SUFFIX"'"}' | jq -r .token)` + "\n")
		for _, section := range members {
			body, err := json.Marshal(map[string]any{
				"slug":       "verify-" + section.Type,
				"title":      "Verify",
				"visibility": section.visibility(),
				"data":       exampleData(section.Fields),
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(b, `curl -fsS -X POST "$API/v1/me/resources/%s" -H "X-M-Stash-App: $KEY" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \`+"\n", section.Type)
			fmt.Fprintf(b, "  -d %s\n", shellQuote(string(body)))
			fmt.Fprintf(b, `curl -fsS "$API/v1/me/resources/%s" -H "X-M-Stash-App: $KEY" -H "Authorization: Bearer $TOKEN"`+"\n", section.Type)
		}
	}
	b.WriteString("```\n\n")
}

// shellQuote wraps a value in single quotes for POSIX shells.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// exampleData builds one plausible value per field for the verify script.
func exampleData(fields []vibeField) map[string]any {
	data := map[string]any{}
	for _, field := range fields {
		data[field.Key] = exampleForKind(field)
	}
	return data
}

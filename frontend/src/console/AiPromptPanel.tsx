// The "Build with AI" tab turns a claimed app into a one-click handoff for an AI
// coding assistant: a ready-to-paste prompt, a Cursor rule file, an agent
// SKILL.md, and a frontend .env — all pre-filled with this app's publishable key
// and API base. A live "ready to ship" checklist nudges the builder toward the
// safe defaults (locked origins, server-only secret) before they go live.

import { useMemo, useState } from "react"
import { Check, Copy, Download, Globe, Sparkles } from "lucide-react"
import type { BuilderApp } from "../api"
import { useToast } from "./toastContext"
import { Highlighted } from "../runner/highlight"
import "./ai-prompt.css"

interface AiPromptPanelProps {
  app: BuilderApp
  apiBase: string
  onAddOrigin?: (origin: string) => void
}

function originChips(): string[] {
  const here = typeof window !== "undefined" ? window.location.origin : ""
  const base = ["http://localhost:5173", "http://localhost:3000"]
  if (here && !base.includes(here)) base.push(here)
  return base
}

export function AiPromptPanel({ app, apiBase, onAddOrigin }: AiPromptPanelProps) {
  const toast = useToast()
  const chips = useMemo(() => originChips(), [])
  const [origin, setOrigin] = useState(chips[0] ?? "http://localhost:5173")
  const pk = app.publishableKey

  const prompt = useMemo(
    () =>
      [
        `I'm building an app and using m-stash for authentication and per-user data.`,
        ``,
        `API base URL: ${apiBase}`,
        `Publishable key (safe in frontend code): ${pk}`,
        ``,
        `On EVERY request: append \`?app=${pk}\` to the URL, set header \`X-M-Stash-App: ${pk}\`,`,
        `and call fetch with { credentials: "omit" } (m-stash never uses cookies from my frontend).`,
        ``,
        `Endpoints:`,
        `- POST /v1/auth/signup  { email, password, username }  -> { token, user, namespace }`,
        `- POST /v1/auth/login   { email, password }            -> { token, user, namespace }`,
        `- GET  /v1/me/resources/{type}                         -> the signed-in user's records`,
        `- POST /v1/me/resources/{type}  { title, content, visibility } -> create a record`,
        `- GET  /v1/shared/resources/{type}                     -> app-wide shared records`,
        ``,
        `After signup/login, keep the returned bearer token and send Authorization: Bearer <token>`,
        `on authed requests. NEVER put a secret key (sk_...) in frontend code — it is server-only.`,
        `The server enforces ownership; the browser never chooses an owner.`,
        ``,
        `Please wire up sign-up, login, and a "save my data" flow against these endpoints.`,
      ].join("\n"),
    [apiBase, pk],
  )

  const cursorRule = useMemo(
    () =>
      [
        `---`,
        `description: m-stash backend (auth + per-user data) for ${app.displayName}`,
        `alwaysApply: true`,
        `---`,
        ``,
        `# m-stash API`,
        ``,
        `Use m-stash for auth and per-user / app-wide data. MongoDB stays behind the API.`,
        ``,
        `- API base: ${apiBase}`,
        `- Publishable key (frontend-safe, names the app): ${pk}`,
        ``,
        `## Every request`,
        ``,
        `- Append \`?app=${pk}\` to the URL and set header \`X-M-Stash-App: ${pk}\`.`,
        `- Call \`fetch(url, { credentials: "omit" })\`. Do not send cookies.`,
        `- After signup/login keep the bearer \`token\`; send \`Authorization: Bearer <token>\` on authed calls.`,
        ``,
        `## Endpoints`,
        ``,
        `- \`POST /v1/auth/signup\` \`{ email, password, username }\` -> \`{ token, user, namespace }\``,
        `- \`POST /v1/auth/login\` \`{ email, password }\` -> \`{ token, user, namespace }\``,
        `- \`GET /v1/me/resources/{type}\` -> the signed-in user's records`,
        `- \`POST /v1/me/resources/{type}\` \`{ title, content, visibility }\` -> create`,
        `- \`GET /v1/shared/resources/{type}\` -> app-wide shared records`,
        ``,
        `## Rules (do not break)`,
        ``,
        `- NEVER put the secret key (\`sk_...\`) in frontend/browser code. It is server-only.`,
        `- The browser never chooses an owner; the server scopes records to the token's user.`,
        `- \`visibility\` is one of "private" (default), "authenticated", or "public".`,
      ].join("\n"),
    [app.displayName, apiBase, pk],
  )

  const skill = useMemo(
    () =>
      [
        `# m-stash integration`,
        ``,
        `Backend for **${app.displayName}**. Provides sign-up, login, and ownership-scoped`,
        `data so this app never needs its own database or security rules.`,
        ``,
        `## Connection`,
        ``,
        `- Base URL: \`${apiBase}\``,
        `- Publishable key (frontend-safe): \`${pk}\``,
        `- Every request: \`?app=${pk}\` + header \`X-M-Stash-App: ${pk}\`, \`credentials: "omit"\`.`,
        ``,
        `## Flow`,
        ``,
        `1. \`POST /v1/auth/signup\` or \`/v1/auth/login\` -> returns \`{ token, user, namespace }\`.`,
        `2. Store the token; send \`Authorization: Bearer <token>\` on authed calls.`,
        `3. \`POST /v1/me/resources/{type}\` to save a record; \`GET\` the same path to read them back.`,
        `4. \`GET /v1/shared/resources/{type}\` for app-wide shared/public data.`,
        ``,
        `## Guardrails`,
        ``,
        `- The secret key (\`sk_...\`) is server-only; never expose it to the browser.`,
        `- The server enforces ownership — a user only ever sees their own private records.`,
      ].join("\n"),
    [app.displayName, apiBase, pk],
  )

  const env = useMemo(
    () =>
      [
        `# m-stash configuration for ${app.displayName}`,
        `# The publishable key is safe to expose in frontend builds — it only names the app.`,
        `VITE_M_STASH_API_URL=${apiBase}`,
        `VITE_M_STASH_APP_KEY=${pk}`,
        ``,
        `# Server-only. Shown once in the console when you create or rotate the app.`,
        `# NEVER commit a real secret key or ship it to the browser.`,
        `# M_STASH_SECRET_KEY=sk_...`,
        ``,
        `# Your app's browser origin (add it under "Allowed browser origins").`,
        `# APP_ORIGIN=${origin}`,
      ].join("\n"),
    [app.displayName, apiBase, pk, origin],
  )

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(`${label} copied`)
    } catch {
      toast.error("Clipboard blocked by the browser")
    }
  }

  function download(filename: string, value: string) {
    const blob = new Blob([value], { type: "text/plain;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = filename
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
    toast.success(`${filename} downloaded`)
  }

  const originLocked = (app.allowedOrigins ?? []).length > 0 && !(app.allowedOrigins ?? []).includes("*")
  const originAdded = (app.allowedOrigins ?? []).includes(origin)
  const checklist: { label: string; done: boolean; hint: string }[] = [
    { label: "App enabled", done: app.active, hint: "Your publishable key is live." },
    { label: "Browser origins locked down", done: originLocked, hint: "Add your site so the key can't be used from anywhere." },
    { label: "Dev origin allow-listed", done: originAdded, hint: `Add ${origin} to allowed origins while you build.` },
    { label: "Secret key kept server-side", done: false, hint: "Only the sk_ key belongs on a trusted server, never the browser." },
  ]

  return (
    <div className="ai-panel">
      <div className="ai-panel-head">
        <p className="eyebrow">
          <Sparkles aria-hidden="true" size={15} /> Hand it to your AI
        </p>
        <p>
          Everything your coding agent needs to wire up m-stash, pre-filled with
          this app's key. Paste the prompt, or drop the files into your repo.
        </p>
      </div>

      <AiCard
        title="Prompt for your AI assistant"
        subtitle="Paste into Cursor, Claude, or any chat."
        lang="curl"
        code={prompt}
        onCopy={() => void copy("Prompt", prompt)}
      />

      <div className="ai-card-grid">
        <AiCard
          title=".cursor/rules/m-stash.mdc"
          subtitle="A Cursor rule so the agent always follows the contract."
          lang="curl"
          code={cursorRule}
          onCopy={() => void copy("Cursor rule", cursorRule)}
          onDownload={() => download("m-stash.mdc", cursorRule)}
          compact
        />
        <AiCard
          title="SKILL.md"
          subtitle="An agent skill describing the same integration."
          lang="curl"
          code={skill}
          onCopy={() => void copy("SKILL.md", skill)}
          onDownload={() => download("SKILL.md", skill)}
          compact
        />
      </div>

      <div className="ai-origin">
        <div className="ai-origin-head">
          <p className="eyebrow">
            <Globe aria-hidden="true" size={14} /> Dev origin
          </p>
          <p>Pick where your app runs in development. It fills the <code>.env</code> and can be allow-listed in one click.</p>
        </div>
        <div className="ai-origin-chips">
          {chips.map((value) => (
            <button
              key={value}
              type="button"
              className={value === origin ? "ai-chip active" : "ai-chip"}
              onClick={() => setOrigin(value)}
            >
              {value}
            </button>
          ))}
          {onAddOrigin ? (
            <button
              type="button"
              className="button secondary small-button"
              disabled={originAdded}
              onClick={() => onAddOrigin(origin)}
            >
              {originAdded ? (
                <>
                  <Check aria-hidden="true" size={14} /> Allow-listed
                </>
              ) : (
                "Add to allowed origins"
              )}
            </button>
          ) : null}
        </div>
      </div>

      <AiCard
        title=".env"
        subtitle="Frontend config. The secret key stays out of it on purpose."
        lang="curl"
        code={env}
        onCopy={() => void copy(".env", env)}
        onDownload={() => download(".env", env)}
        compact
      />

      <div className="ai-checklist">
        <p className="eyebrow">Ready to ship</p>
        <ul>
          {checklist.map((item) => (
            <li key={item.label} className={item.done ? "done" : ""}>
              <span className="ai-check" aria-hidden="true">
                {item.done ? <Check size={13} /> : null}
              </span>
              <span>
                <strong>{item.label}</strong>
                <small>{item.hint}</small>
              </span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function AiCard({
  title,
  subtitle,
  code,
  lang,
  onCopy,
  onDownload,
  compact,
}: {
  title: string
  subtitle: string
  code: string
  lang: "json" | "curl" | "fetch"
  onCopy: () => void
  onDownload?: () => void
  compact?: boolean
}) {
  return (
    <div className="ai-card">
      <div className="ai-card-head">
        <div>
          <h4>{title}</h4>
          <p>{subtitle}</p>
        </div>
        <div className="ai-card-actions">
          {onDownload ? (
            <button type="button" className="icon-button" aria-label={`Download ${title}`} onClick={onDownload}>
              <Download aria-hidden="true" size={15} />
            </button>
          ) : null}
          <button type="button" className="icon-button" aria-label={`Copy ${title}`} onClick={onCopy}>
            <Copy aria-hidden="true" size={15} />
          </button>
        </div>
      </div>
      <pre className={compact ? "ai-code compact" : "ai-code"}>
        <Highlighted code={code} lang={lang} />
      </pre>
    </div>
  )
}

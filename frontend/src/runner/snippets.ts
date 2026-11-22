// Pure helpers shared by the API loop runner: the canonical description of each
// step, how it turns into a request, and how it renders as copy-paste cURL and
// fetch(). Keeping this pure (no React, no network) makes the loop easy to reason
// about and keeps the runner and the copied snippets perfectly in sync.

import { withAppQuery } from "./client"

export type StepId = "register" | "login" | "create" | "viewYours" | "viewPublic"

export type Visibility = "private" | "authenticated" | "public"

export interface RunnerContext {
  base: string
  pk: string
  email: string
  password: string
  username: string
  type: string
  slug: string
  title: string
  visibility: Visibility
  data: Record<string, unknown>
  /** Bearer token captured from Register or Log in. */
  token?: string
}

export interface ResolvedStep {
  method: string
  /** Path WITHOUT the ?app= query; callers add it for display or execution. */
  path: string
  body?: unknown
  /** True when the request must carry a bearer token. */
  authed: boolean
}

export interface StepMeta {
  id: StepId
  title: string
  purpose: string
}

export const STEP_META: StepMeta[] = [
  { id: "register", title: "Register", purpose: "Create a user. The response includes a bearer token." },
  { id: "login", title: "Log in", purpose: "Exchange email + password for a fresh bearer token." },
  { id: "create", title: "Create a record", purpose: "Save a record owned by the signed-in user." },
  { id: "viewYours", title: "View yours", purpose: "Read your own record back with your token." },
  { id: "viewPublic", title: "View public", purpose: "Read the record with no auth, as anyone would." },
]

// normalizeType mirrors the server + console rule: a trimmed, lowercased bucket.
export function normalizeType(value: string): string {
  return value.trim().toLowerCase()
}

// normalizeSlug keeps slugs and usernames to the server's [a-z0-9-] shape.
export function normalizeSlug(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
}

export function resolveStep(id: StepId, ctx: RunnerContext): ResolvedStep {
  const type = normalizeType(ctx.type)
  const slug = normalizeSlug(ctx.slug)
  const username = normalizeSlug(ctx.username)
  switch (id) {
    case "register":
      return {
        method: "POST",
        path: "/v1/auth/signup",
        body: { email: ctx.email, password: ctx.password, username },
        authed: false,
      }
    case "login":
      return {
        method: "POST",
        path: "/v1/auth/login",
        body: { email: ctx.email, password: ctx.password },
        authed: false,
      }
    case "create":
      return {
        method: "POST",
        path: `/v1/me/resources/${encodeURIComponent(type)}`,
        body: { slug, title: ctx.title, data: ctx.data, visibility: ctx.visibility },
        authed: true,
      }
    case "viewYours":
      return {
        method: "GET",
        path: `/v1/me/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`,
        authed: true,
      }
    case "viewPublic":
      return {
        method: "GET",
        path: `/v1/public/${encodeURIComponent(username)}/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`,
        authed: false,
      }
  }
}

/** A PUT that flips an existing record to public, for the teaching-moment re-run. */
export function resolveMakePublic(ctx: RunnerContext): ResolvedStep {
  const type = normalizeType(ctx.type)
  const slug = normalizeSlug(ctx.slug)
  return {
    method: "PUT",
    path: `/v1/me/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`,
    body: { slug, title: ctx.title, data: ctx.data, visibility: "public" },
    authed: true,
  }
}

interface SnippetOptions {
  /** When true, authed snippets print $TOKEN instead of the live bearer token. */
  maskToken?: boolean
}

function tokenLiteral(ctx: RunnerContext, options: SnippetOptions): string {
  if (options.maskToken || !ctx.token) return "$TOKEN"
  return ctx.token
}

export function buildCurl(id: StepId, ctx: RunnerContext, options: SnippetOptions = {}): string {
  const step = resolveStep(id, ctx)
  const url = `${ctx.base.replace(/\/+$/, "")}${withAppQuery(step.path, ctx.pk)}`
  const lines = [`curl -X ${step.method} "${url}"`, `  -H "X-M-Stash-App: ${ctx.pk}"`]
  if (step.authed) lines.push(`  -H "Authorization: Bearer ${tokenLiteral(ctx, options)}"`)
  if (step.body !== undefined) {
    lines.push(`  -H "Content-Type: application/json"`)
    lines.push(`  -d '${JSON.stringify(step.body)}'`)
  }
  return lines.join(" \\\n")
}

export function buildFetch(id: StepId, ctx: RunnerContext): string {
  const step = resolveStep(id, ctx)
  const url = `${ctx.base.replace(/\/+$/, "")}${withAppQuery(step.path, ctx.pk)}`
  const headerLines = [`    "X-M-Stash-App": "${ctx.pk}",`]
  // The fetch snippet always references a `token` variable the caller fills from
  // the Register/Log in response, rather than inlining a literal bearer token.
  if (step.authed) headerLines.push("    \"Authorization\": `Bearer ${token}`,")
  if (step.body !== undefined) headerLines.push(`    "Content-Type": "application/json",`)
  const initLines = [`  method: "${step.method}",`, `  headers: {`, ...headerLines, `  },`]
  if (step.body !== undefined) initLines.push(`  body: JSON.stringify(${JSON.stringify(step.body, null, 2).replace(/\n/g, "\n  ")}),`)
  // credentials: "omit" is the whole point — a generated frontend never relies on cookies.
  initLines.push(`  credentials: "omit",`)
  return `const response = await fetch(\n  "${url}",\n  {\n${initLines.map((line) => `  ${line}`).join("\n")}\n  }\n);\nconst body = await response.json();`
}

// explainError turns a status code into a one-line, teachable reason specific to
// the step that failed. Returns an empty string when the status is a success.
export function explainError(id: StepId, status: number, ctx: RunnerContext): string {
  if (status >= 200 && status < 300) return ""
  if (status === 0) return "The request never reached the server — check the API base URL and that the app key is valid."
  switch (status) {
    case 400:
      if (id === "register") return "The email, password (12-72 chars), or username ([a-z0-9-], 3-32 chars) didn't pass validation."
      if (id === "create") return "The slug must be 3-32 chars of [a-z0-9-]. Fix the slug and re-run."
      return "A field didn't pass validation. Check the inputs above."
    case 401:
      return "No valid token. Run Register or Log in first, then re-run this step."
    case 403:
      return "The token isn't allowed to write here. Personal records are written by their owner only."
    case 404:
      if (id === "viewPublic" && ctx.visibility === "private")
        return "Working as intended: a private record is invisible to the public endpoint. Make it public and re-run to see it appear."
      if (id === "viewPublic") return "Not found at the public URL — check the username, type, and slug all match what you created."
      return "Not found — the key is wrong, or the record doesn't exist yet."
    case 409:
      if (id === "register") return "That email is already registered. Regenerate the identity and try again."
      return "That slug already exists for this type. Update it instead with a PUT."
    case 413:
      return "The record is larger than this plan or sandbox allows."
    case 429:
      return "You hit a rate or plan limit — the sandbox or free-tier cap for this resource."
    default:
      return `The request failed with status ${status}.`
  }
}

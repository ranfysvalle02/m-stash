// A tiny, self-contained HTTP client for the API loop runner. It deliberately
// does NOT reuse frontend/src/api.ts: that module carries the console's cookie
// session and CSRF token. The runner instead talks to a sandbox or a builder's
// app exactly the way a generated frontend would — bearer tokens only, no
// cookies — so what the user sees in the runner is what their own code will do.

export interface RunnerFetchInput {
  /** API origin, e.g. https://m-stash.example (no trailing slash). */
  base: string
  /** Publishable key (pk_...). Safe in the browser; names the app. */
  pk: string
  /** Bearer token from a prior Register/Log in step, when the call is authed. */
  token?: string
  method: string
  /** Path beginning with /v1/...; the ?app=<pk> query is appended here. */
  path: string
  body?: unknown
}

export interface RunnerResult {
  status: number
  ms: number
  json: unknown
  /** Set when the request never produced an HTTP status (network/CORS failure). */
  networkError?: string
}

/** Append ?app=<pk> to a path, preserving any existing query string. */
export function withAppQuery(path: string, pk: string): string {
  const separator = path.includes("?") ? "&" : "?"
  return `${path}${separator}app=${encodeURIComponent(pk)}`
}

/** Join an origin and a path without doubling or dropping the slash. */
export function joinBase(base: string, path: string): string {
  const trimmedBase = base.replace(/\/+$/, "")
  const trimmedPath = path.startsWith("/") ? path : `/${path}`
  return `${trimmedBase}${trimmedPath}`
}

/**
 * Build the request URL, collapsing a same-origin base to a relative path so the
 * browser resolves it against the document's own origin. This keeps the live
 * call working even when a reverse proxy stamped the base with the wrong scheme
 * (e.g. http:// behind HTTPS termination). A cross-origin base is joined as-is.
 */
export function runnerURL(base: string, pathWithQuery: string): string {
  if (typeof window !== "undefined") {
    try {
      const parsed = new URL(base, window.location.origin)
      if (parsed.host === window.location.host) {
        return pathWithQuery.startsWith("/") ? pathWithQuery : `/${pathWithQuery}`
      }
    } catch {
      // Fall through to the absolute join below.
    }
  }
  return joinBase(base, pathWithQuery)
}

export async function runnerFetch(input: RunnerFetchInput): Promise<RunnerResult> {
  const url = runnerURL(input.base, withAppQuery(input.path, input.pk))
  const headers: Record<string, string> = {
    // Every request names its app; the server derives ownership from the token.
    "X-M-Stash-App": input.pk,
  }
  if (input.token) headers.Authorization = `Bearer ${input.token}`
  if (input.body !== undefined) headers["Content-Type"] = "application/json"

  const startedAt = performance.now()
  try {
    const response = await fetch(url, {
      method: input.method,
      headers,
      body: input.body === undefined ? undefined : JSON.stringify(input.body),
      // No cookies, ever: this mirrors a real third-party frontend and keeps the
      // console's own session untouched.
      credentials: "omit",
    })
    const ms = Math.round(performance.now() - startedAt)
    const json = await response.json().catch(() => undefined)
    return { status: response.status, ms, json }
  } catch (error) {
    const ms = Math.round(performance.now() - startedAt)
    const message = error instanceof Error ? error.message : "Network request failed"
    return { status: 0, ms, json: undefined, networkError: message }
  }
}

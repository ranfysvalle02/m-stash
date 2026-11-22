import { useCallback, useMemo, useState } from "react"
import type { ReactNode } from "react"
import { Copy, Globe, KeyRound, Lock, ShieldAlert, ShieldCheck } from "lucide-react"
import { runnerFetch, withAppQuery } from "./client"
import {
  STEP_META,
  buildCurl,
  buildFetch,
  explainError,
  normalizeSlug,
  normalizeType,
  resolveMakePublic,
  resolveStep,
} from "./snippets"
import type { RunnerContext, StepId, Visibility } from "./snippets"
import { Highlighted } from "./highlight"
import { useCountdown } from "./useCountdown"
import { useToast } from "../console/toastContext"
import "./runner.css"

type StepStatus = "idle" | "running" | "ok" | "error"

interface StepState {
  status: StepStatus
  httpStatus?: number
  ms?: number
  response?: unknown
  error?: string
}

/** The resolved live target: a publishable key plus the API base to call. */
interface ActiveSandbox {
  pk: string
  base: string
  expiresAt?: string
}

interface ApiLoopRunnerProps {
  /** API base to call. Falls back to the current origin when omitted. */
  base?: string
  /** Publishable key. When omitted, onEnsureSandbox provisions one on first run. */
  pk?: string
  mode: "sandbox" | "app"
  appName: string
  expiresAt?: string
  /** Rendered after the loop succeeds; e.g. a "Keep this app" call to action. */
  onKeep?: ReactNode
  /**
   * Lazily provisions a sandbox the first time the visitor runs, so the runner
   * can render its steps immediately and spin up a real backend on demand.
   */
  onEnsureSandbox?: () => Promise<ActiveSandbox>
}

const STEP_ORDER: StepId[] = ["register", "login", "create", "viewYours", "viewPublic"]

interface IsolationState {
  status: "idle" | "running" | "done" | "error"
  intruder?: string
  readStatus?: number
  writeStatus?: number
  error?: string
}

function randomHex(bytes: number): string {
  const buffer = new Uint8Array(bytes)
  crypto.getRandomValues(buffer)
  return Array.from(buffer, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

function strongPassword(): string {
  // 24 hex chars clears the server's 12-72 character rule with plenty of entropy.
  return `Pw-${randomHex(11)}`
}

function emptyStepState(): Record<StepId, StepState> {
  return {
    register: { status: "idle" },
    login: { status: "idle" },
    create: { status: "idle" },
    viewYours: { status: "idle" },
    viewPublic: { status: "idle" },
  }
}

export function ApiLoopRunner({ base, pk, mode, appName, expiresAt, onKeep, onEnsureSandbox }: ApiLoopRunnerProps) {
  const toast = useToast()
  const [email, setEmail] = useState(() => `you+${randomHex(3)}@example.com`)
  const [password, setPassword] = useState(() => strongPassword())
  const [username, setUsername] = useState(() => `builder-${randomHex(3)}`)
  const [showPassword, setShowPassword] = useState(false)
  const [showInputs, setShowInputs] = useState(false)
  const [type, setType] = useState("note")
  const [slug, setSlug] = useState("first")
  const [title, setTitle] = useState("My first record")
  const [visibility, setVisibility] = useState<Visibility>("private")
  const [dataText, setDataText] = useState('{\n  "body": "Hello from the runner"\n}')
  const [includeToken, setIncludeToken] = useState(false)
  const [token, setToken] = useState<string | undefined>(undefined)
  const [steps, setSteps] = useState<Record<StepId, StepState>>(emptyStepState)
  const [running, setRunning] = useState(false)
  const [snippetTab, setSnippetTab] = useState<Record<StepId, "curl" | "fetch">>({
    register: "curl", login: "curl", create: "curl", viewYours: "curl", viewPublic: "curl",
  })
  const [provisioned, setProvisioned] = useState<ActiveSandbox | null>(() =>
    pk ? { pk, base: base || window.location.origin, expiresAt } : null,
  )
  const [provisionError, setProvisionError] = useState("")
  const [isolation, setIsolation] = useState<IsolationState>({ status: "idle" })

  const activeBase = provisioned?.base ?? base ?? window.location.origin
  const activePk = provisioned?.pk ?? pk ?? ""
  const expiry = useCountdown(provisioned?.expiresAt ?? expiresAt)

  const parsedData = useMemo<{ value: Record<string, unknown>; error?: string }>(() => {
    try {
      const value = JSON.parse(dataText)
      if (value === null || typeof value !== "object" || Array.isArray(value)) {
        return { value: {}, error: "Data must be a JSON object." }
      }
      return { value: value as Record<string, unknown> }
    } catch {
      return { value: {}, error: "Data is not valid JSON." }
    }
  }, [dataText])

  const context = useMemo<RunnerContext>(
    () => ({ base: activeBase, pk: activePk, email, password, username, type, slug, title, visibility, data: parsedData.value, token }),
    [activeBase, activePk, email, password, username, type, slug, title, visibility, parsedData.value, token],
  )

  // In sandbox mode the token is throwaway, so snippets inline it; in app mode we
  // mask it behind $TOKEN unless the builder explicitly opts to include it.
  const maskToken = mode === "app" && !includeToken

  const allDone = STEP_ORDER.every((id) => steps[id].status === "ok")
  const recordExists = steps.create.status === "ok"

  // ensureActive resolves the live target, provisioning a sandbox on first use.
  const ensureActive = useCallback(async (): Promise<ActiveSandbox | null> => {
    if (provisioned) return provisioned
    if (!onEnsureSandbox) {
      return activePk ? { pk: activePk, base: activeBase } : null
    }
    try {
      const result = await onEnsureSandbox()
      const next: ActiveSandbox = { pk: result.pk, base: result.base || window.location.origin, expiresAt: result.expiresAt }
      setProvisioned(next)
      setProvisionError("")
      return next
    } catch (error) {
      setProvisionError(error instanceof Error ? error.message : "Could not start a sandbox right now.")
      return null
    }
  }, [provisioned, onEnsureSandbox, activePk, activeBase])

  const runStep = useCallback(
    async (id: StepId, ctx: RunnerContext): Promise<{ ok: boolean; token?: string }> => {
      const step = resolveStep(id, ctx)
      if (step.authed && !ctx.token) {
        setSteps((prev) => ({ ...prev, [id]: { status: "error", error: explainError(id, 401, ctx) } }))
        return { ok: false }
      }
      setSteps((prev) => ({ ...prev, [id]: { status: "running" } }))
      const result = await runnerFetch({ base: ctx.base, pk: ctx.pk, token: step.authed ? ctx.token : undefined, method: step.method, path: step.path, body: step.body })
      const ok = result.status >= 200 && result.status < 300
      const nextToken = ok ? extractToken(result.json) : undefined
      setSteps((prev) => ({
        ...prev,
        [id]: {
          status: ok ? "ok" : "error",
          httpStatus: result.status,
          ms: result.ms,
          response: result.json,
          error: ok ? undefined : result.networkError ?? explainError(id, result.status, ctx),
        },
      }))
      if (nextToken) setToken(nextToken)
      return { ok, token: nextToken }
    },
    [],
  )

  const handleRunStep = useCallback(
    async (id: StepId) => {
      if (parsedData.error && id === "create") return
      setRunning(true)
      const active = await ensureActive()
      if (active) await runStep(id, { ...context, base: active.base, pk: active.pk })
      setRunning(false)
    },
    [context, ensureActive, parsedData.error, runStep],
  )

  const handleRunAll = useCallback(async () => {
    if (parsedData.error || running) return
    setRunning(true)
    setSteps(emptyStepState())
    setToken(undefined)
    setIsolation({ status: "idle" })
    const active = await ensureActive()
    if (!active) {
      setRunning(false)
      return
    }
    // Thread the freshly-minted token through Create and View yours without
    // waiting for React state to settle between steps.
    let liveToken: string | undefined = undefined
    for (const id of STEP_ORDER) {
      const outcome = await runStep(id, { ...context, base: active.base, pk: active.pk, token: liveToken })
      if (outcome.token) liveToken = outcome.token
      if (!outcome.ok) break
    }
    setRunning(false)
  }, [context, ensureActive, parsedData.error, running, runStep])

  const handleRegenerateIdentity = useCallback(() => {
    setEmail(`you+${randomHex(3)}@example.com`)
    setUsername(`builder-${randomHex(3)}`)
    setPassword(strongPassword())
    setToken(undefined)
    setSteps(emptyStepState())
    setIsolation({ status: "idle" })
  }, [])

  const handleMakePublicAndRerun = useCallback(async () => {
    setRunning(true)
    setVisibility("public")
    const publicCtx: RunnerContext = { ...context, base: activeBase, pk: activePk, visibility: "public", token }
    const put = resolveMakePublic(publicCtx)
    setSteps((prev) => ({ ...prev, viewPublic: { status: "running" } }))
    const update = await runnerFetch({ base: publicCtx.base, pk: publicCtx.pk, token, method: put.method, path: put.path, body: put.body })
    if (update.status < 200 || update.status >= 300) {
      setSteps((prev) => ({ ...prev, viewPublic: { status: "error", httpStatus: update.status, ms: update.ms, response: update.json, error: explainError("create", update.status, publicCtx) } }))
      setRunning(false)
      return
    }
    await runStep("viewPublic", publicCtx)
    setRunning(false)
  }, [context, activeBase, activePk, runStep, token])

  // runIsolationCheck proves server-enforced ownership: a second, unrelated user
  // can neither read the first user's private record (404) nor write shared data
  // (403). Both are live calls against the same app.
  const runIsolationCheck = useCallback(async () => {
    if (!recordExists) return
    setIsolation({ status: "running" })
    const intruderName = `intruder-${randomHex(3)}`
    const intruderEmail = `intruder+${randomHex(3)}@example.com`
    const normalizedType = normalizeType(type)
    const normalizedSlug = normalizeSlug(slug)
    const register = await runnerFetch({
      base: activeBase, pk: activePk, method: "POST", path: "/v1/auth/signup",
      body: { email: intruderEmail, password: strongPassword(), username: intruderName },
    })
    const intruderToken = extractToken(register.json)
    if (!intruderToken) {
      setIsolation({ status: "error", error: "Could not create the second test user to run the check." })
      return
    }
    const read = await runnerFetch({
      base: activeBase, pk: activePk, token: intruderToken, method: "GET",
      path: `/v1/me/resources/${encodeURIComponent(normalizedType)}/${encodeURIComponent(normalizedSlug)}`,
    })
    const write = await runnerFetch({
      base: activeBase, pk: activePk, token: intruderToken, method: "POST",
      path: `/v1/shared/resources/${encodeURIComponent(normalizedType)}`,
      body: { title: "hijack attempt", data: {}, visibility: "public" },
    })
    setIsolation({ status: "done", intruder: intruderName, readStatus: read.status, writeStatus: write.status })
  }, [recordExists, type, slug, activeBase, activePk])

  const copy = useCallback(
    async (label: string, value: string) => {
      try {
        await navigator.clipboard.writeText(value)
        toast.success(`${label} copied`)
      } catch {
        toast.error("Copy failed")
      }
    },
    [toast],
  )

  const runLabel = running ? (provisioned ? "Running…" : "Starting…") : "Run all 5 steps"

  return (
    <div
      className="runner"
      data-mode={mode}
      onKeyDown={(event) => {
        if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
          event.preventDefault()
          void handleRunAll()
        }
      }}
    >
      <header className="runner-head">
        <div>
          <p className="eyebrow">Run the whole loop</p>
          <h3>Register → Log in → Create → View yours → View public</h3>
          <p className="runner-sub">
            Live calls to <strong>{appName}</strong>. Each sends <code>?app={activePk ? `${activePk.slice(0, 10)}…` : "pk_…"}</code> and a bearer token —
            no cookies. The <code>pk_</code> key is not a secret; it only names the app, and the server enforces ownership on every request.
          </p>
        </div>
        <div className="runner-head-side">
          <span className={`runner-token-pill${token ? " live" : ""}`}>
            <KeyRound aria-hidden="true" size={13} />
            {token ? `Bearer ${token.slice(0, 8)}… · live` : "No token yet"}
          </span>
          {expiry && mode === "sandbox" ? (
            <span className="runner-chip">
              {expiry}
              {onKeep ? " · Keep ↓" : ""}
            </span>
          ) : null}
        </div>
      </header>

      {mode === "app" ? (
        <p className="runner-banner">This creates a real user in <strong>{appName}</strong>. Use a disposable test email.</p>
      ) : null}

      <div className="runner-summary">
        <div className="runner-summary-text">
          <span>Acting as <strong>@{normalizeSlug(username) || "user"}</strong></span>
          <span className="runner-summary-dot">·</span>
          <span><code>{normalizeType(type) || "type"}/{normalizeSlug(slug) || "slug"}</code></span>
          <span className="runner-summary-dot">·</span>
          <span className={`runner-vis runner-vis-${visibility}`}>{visibility}</span>
        </div>
        <div className="runner-summary-actions">
          <button type="button" className="runner-link" onClick={() => setShowInputs((value) => !value)}>
            {showInputs ? "Hide payload" : "Edit payload"}
          </button>
          <button type="button" className="runner-link" onClick={handleRegenerateIdentity}>New identity</button>
        </div>
      </div>

      {showInputs ? (
        <div className="runner-inputs">
          <label>
            <span>Email</span>
            <input value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="off" />
          </label>
          <label>
            <span>Password</span>
            <span className="runner-password">
              <input type={showPassword ? "text" : "password"} value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="off" />
              <button type="button" className="runner-link" onClick={() => setShowPassword((value) => !value)}>
                {showPassword ? "Hide" : "Show"}
              </button>
            </span>
          </label>
          <label>
            <span>Username</span>
            <input value={username} onChange={(event) => setUsername(normalizeSlug(event.target.value))} autoComplete="off" />
          </label>
          <label>
            <span>Type</span>
            <input value={type} onChange={(event) => setType(normalizeType(event.target.value))} autoComplete="off" />
          </label>
          <label>
            <span>Slug</span>
            <input value={slug} onChange={(event) => setSlug(normalizeSlug(event.target.value))} autoComplete="off" />
          </label>
          <label>
            <span>Title</span>
            <input value={title} onChange={(event) => setTitle(event.target.value)} autoComplete="off" />
          </label>
          <label>
            <span>Visibility</span>
            <select value={visibility} onChange={(event) => setVisibility(event.target.value as Visibility)}>
              <option value="private">private — owner only</option>
              <option value="authenticated">authenticated — any signed-in user</option>
              <option value="public">public — anyone</option>
            </select>
          </label>
          <label className="runner-data">
            <span>Data (JSON)</span>
            <textarea value={dataText} onChange={(event) => setDataText(event.target.value)} rows={4} spellCheck={false} />
            {parsedData.error ? <small className="runner-error">{parsedData.error}</small> : null}
          </label>
        </div>
      ) : null}

      <div className="runner-actions">
        <button type="button" className="button primary" onClick={handleRunAll} disabled={running || Boolean(parsedData.error)}>
          {runLabel}
        </button>
        <span className="runner-shortcut">⌘/Ctrl + Enter</span>
        {mode === "app" ? (
          <label className="runner-toggle">
            <input type="checkbox" checked={includeToken} onChange={(event) => setIncludeToken(event.target.checked)} />
            <span>Include my live token in copied snippets</span>
          </label>
        ) : null}
      </div>

      {provisionError ? <p className="runner-error runner-provision-error">{provisionError}</p> : null}

      <ol className="runner-steps">
        {STEP_META.map((meta) => {
          const state = steps[meta.id]
          const tab = snippetTab[meta.id]
          const resolved = resolveStep(meta.id, context)
          const teaching = meta.id === "viewPublic" && state.status === "error" && state.httpStatus === 404 && visibility === "private"
          const snippet = tab === "curl" ? buildCurl(meta.id, context, { maskToken }) : buildFetch(meta.id, context)
          return (
            <li key={meta.id} className="runner-step" data-status={state.status}>
              <div className="runner-step-head">
                <div className="runner-step-title">
                  <span className="runner-method">{resolved.method}</span>
                  <strong>{meta.title}</strong>
                  <code>{resolved.path}</code>
                </div>
                <div className="runner-step-right">
                  <span className={`runner-auth-badge${resolved.authed ? " authed" : ""}`}>
                    {resolved.authed ? <Lock aria-hidden="true" size={11} /> : <Globe aria-hidden="true" size={11} />}
                    {resolved.authed ? "Bearer" : "No auth"}
                  </span>
                  {state.status === "ok" || state.status === "error" ? (
                    <span className={`runner-pill ${state.status}`}>
                      {state.httpStatus} · {state.ms}ms
                    </span>
                  ) : null}
                  <button type="button" className="button secondary runner-run" onClick={() => handleRunStep(meta.id)} disabled={running}>
                    Run
                  </button>
                </div>
              </div>
              <p className="runner-purpose">{meta.purpose}</p>

              {state.error ? (
                <div className={`runner-explain ${teaching ? "teaching" : ""}`}>
                  <p>{state.error}</p>
                  {meta.id === "register" && state.httpStatus === 409 ? (
                    <button type="button" className="runner-link" onClick={handleRegenerateIdentity}>Regenerate identity</button>
                  ) : null}
                  {teaching ? (
                    <button type="button" className="runner-link" onClick={handleMakePublicAndRerun} disabled={running}>
                      Make it public and re-run
                    </button>
                  ) : null}
                </div>
              ) : null}

              {state.response !== undefined ? (
                <details className="runner-response">
                  <summary>Response</summary>
                  <div className="runner-code-wrap">
                    <button type="button" className="runner-copy" onClick={() => copy("Response", JSON.stringify(state.response, null, 2))} aria-label="Copy response">
                      <Copy aria-hidden="true" size={13} />
                    </button>
                    <pre><Highlighted code={JSON.stringify(state.response, null, 2)} lang="json" /></pre>
                  </div>
                </details>
              ) : null}

              <div className="runner-snippet">
                <div className="runner-tabs">
                  <button type="button" className={tab === "curl" ? "active" : ""} onClick={() => setSnippetTab((prev) => ({ ...prev, [meta.id]: "curl" }))}>cURL</button>
                  <button type="button" className={tab === "fetch" ? "active" : ""} onClick={() => setSnippetTab((prev) => ({ ...prev, [meta.id]: "fetch" }))}>fetch</button>
                </div>
                <div className="runner-code-wrap">
                  <button type="button" className="runner-copy" onClick={() => copy(tab === "curl" ? "cURL" : "fetch", snippet)} aria-label="Copy snippet">
                    <Copy aria-hidden="true" size={13} />
                  </button>
                  <pre className="runner-code"><Highlighted code={snippet} lang={tab === "curl" ? "curl" : "fetch"} /></pre>
                </div>
              </div>
            </li>
          )
        })}
      </ol>

      {recordExists ? (
        <div className={`runner-isolation${isolation.status === "done" ? " proven" : ""}`} data-status={isolation.status}>
          <div className="runner-isolation-head">
            <div>
              <p className="eyebrow"><ShieldCheck aria-hidden="true" size={14} /> Prove it is really locked down</p>
              <p className="runner-isolation-sub">
                Spin up a second, unrelated user and watch the server reject them — no rules written by you.
              </p>
            </div>
            <button type="button" className="button secondary" onClick={runIsolationCheck} disabled={isolation.status === "running"}>
              {isolation.status === "running" ? "Checking…" : "Prove ownership isolation"}
            </button>
          </div>

          {isolation.status === "done" ? (
            <div className="runner-isolation-results">
              <span className={`runner-verdict${isolation.readStatus === 404 ? " ok" : " warn"}`}>
                {isolation.readStatus === 404 ? <ShieldCheck aria-hidden="true" size={14} /> : <ShieldAlert aria-hidden="true" size={14} />}
                {isolation.readStatus} · Read of your private record {isolation.readStatus === 404 ? "blocked" : "unexpected"}
              </span>
              <span className={`runner-verdict${isolation.writeStatus === 403 ? " ok" : " warn"}`}>
                {isolation.writeStatus === 403 ? <ShieldCheck aria-hidden="true" size={14} /> : <ShieldAlert aria-hidden="true" size={14} />}
                {isolation.writeStatus} · Shared write by a stranger {isolation.writeStatus === 403 ? "rejected" : "unexpected"}
              </span>
              <div className="runner-code-wrap">
                <button
                  type="button"
                  className="runner-copy"
                  onClick={() =>
                    copy(
                      "Isolation cURL",
                      `# As an unrelated user (${isolation.intruder}), with their own bearer token:\n` +
                        `curl "${activeBase.replace(/\/+$/, "")}${withAppQuery(`/v1/me/resources/${normalizeType(type)}/${normalizeSlug(slug)}`, activePk)}" \\\n  -H "X-M-Stash-App: ${activePk}" \\\n  -H "Authorization: Bearer $INTRUDER_TOKEN"   # -> 404\n\n` +
                        `curl -X POST "${activeBase.replace(/\/+$/, "")}${withAppQuery(`/v1/shared/resources/${normalizeType(type)}`, activePk)}" \\\n  -H "X-M-Stash-App: ${activePk}" \\\n  -H "Authorization: Bearer $INTRUDER_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '{"title":"hijack","data":{},"visibility":"public"}'   # -> 403`,
                    )
                  }
                  aria-label="Copy isolation cURL"
                >
                  <Copy aria-hidden="true" size={13} />
                </button>
                <pre className="runner-code"><Highlighted
                  code={`# As an unrelated user (${isolation.intruder}), with their own bearer token:\ncurl "${activeBase.replace(/\/+$/, "")}${withAppQuery(`/v1/me/resources/${normalizeType(type)}/${normalizeSlug(slug)}`, activePk)}" \\\n  -H "Authorization: Bearer $INTRUDER_TOKEN"   # -> 404\n\ncurl -X POST "${activeBase.replace(/\/+$/, "")}${withAppQuery(`/v1/shared/resources/${normalizeType(type)}`, activePk)}" \\\n  -H "Authorization: Bearer $INTRUDER_TOKEN" ...   # -> 403`}
                  lang="curl"
                /></pre>
              </div>
            </div>
          ) : null}
          {isolation.status === "error" ? <p className="runner-error">{isolation.error}</p> : null}
        </div>
      ) : null}

      {allDone && onKeep ? <div className="runner-keep">{onKeep}</div> : null}
    </div>
  )
}

function extractToken(json: unknown): string | undefined {
  if (json && typeof json === "object" && "token" in json) {
    const token = (json as { token?: unknown }).token
    if (typeof token === "string" && token) return token
  }
  return undefined
}

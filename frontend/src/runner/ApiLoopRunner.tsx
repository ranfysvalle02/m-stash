import { useCallback, useMemo, useState } from "react"
import type { ReactNode } from "react"
import { runnerFetch } from "./client"
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
import "./runner.css"

type StepStatus = "idle" | "running" | "ok" | "error"

interface StepState {
  status: StepStatus
  httpStatus?: number
  ms?: number
  response?: unknown
  error?: string
}

interface ApiLoopRunnerProps {
  base: string
  pk: string
  mode: "sandbox" | "app"
  appName: string
  expiresAt?: string
  /** Rendered after the loop succeeds; e.g. a "Keep this app" call to action. */
  onKeep?: ReactNode
}

const STEP_ORDER: StepId[] = ["register", "login", "create", "viewYours", "viewPublic"]

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

function expiryLabel(expiresAt?: string): string {
  if (!expiresAt) return ""
  const when = new Date(expiresAt)
  if (Number.isNaN(when.getTime())) return ""
  return when.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
}

export function ApiLoopRunner({ base, pk, mode, appName, expiresAt, onKeep }: ApiLoopRunnerProps) {
  const [email, setEmail] = useState(() => `you+${randomHex(3)}@example.com`)
  const [password, setPassword] = useState(() => strongPassword())
  const [username, setUsername] = useState(() => `builder-${randomHex(3)}`)
  const [showPassword, setShowPassword] = useState(false)
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
    () => ({ base, pk, email, password, username, type, slug, title, visibility, data: parsedData.value, token }),
    [base, pk, email, password, username, type, slug, title, visibility, parsedData.value, token],
  )

  // In sandbox mode the token is throwaway, so snippets inline it; in app mode we
  // mask it behind $TOKEN unless the builder explicitly opts to include it.
  const maskToken = mode === "app" && !includeToken

  const allDone = STEP_ORDER.every((id) => steps[id].status === "ok")

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
      await runStep(id, context)
      setRunning(false)
    },
    [context, parsedData.error, runStep],
  )

  const handleRunAll = useCallback(async () => {
    if (parsedData.error) return
    setRunning(true)
    setSteps(emptyStepState())
    setToken(undefined)
    // Thread the freshly-minted token through Create and View yours without
    // waiting for React state to settle between steps.
    let liveToken: string | undefined = undefined
    for (const id of STEP_ORDER) {
      const outcome = await runStep(id, { ...context, token: liveToken })
      if (outcome.token) liveToken = outcome.token
      if (!outcome.ok) break
    }
    setRunning(false)
  }, [context, parsedData.error, runStep])

  const handleRegenerateIdentity = useCallback(() => {
    setEmail(`you+${randomHex(3)}@example.com`)
    setUsername(`builder-${randomHex(3)}`)
    setPassword(strongPassword())
    setToken(undefined)
    setSteps(emptyStepState())
  }, [])

  const handleMakePublicAndRerun = useCallback(async () => {
    setRunning(true)
    setVisibility("public")
    const publicCtx: RunnerContext = { ...context, visibility: "public", token }
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
  }, [context, runStep, token])

  const expiry = expiryLabel(expiresAt)

  return (
    <div className="runner" data-mode={mode}>
      <header className="runner-head">
        <div>
          <p className="eyebrow">Run the whole loop</p>
          <h3>Register → Log in → Create → View yours → View public</h3>
          <p className="runner-sub">
            Live calls to <strong>{appName}</strong>. Each sends <code>?app={pk.slice(0, 10)}…</code> and a bearer token —
            no cookies. The <code>pk_</code> key is not a secret; it only names the app, and the server enforces ownership on every request.
          </p>
        </div>
        {expiry && mode === "sandbox" ? <span className="runner-chip">Sandbox expires {expiry}</span> : null}
      </header>

      {mode === "app" ? (
        <p className="runner-banner">This creates a real user in <strong>{appName}</strong>. Use a disposable test email.</p>
      ) : null}

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

      <div className="runner-actions">
        <button type="button" className="button primary" onClick={handleRunAll} disabled={running || Boolean(parsedData.error)}>
          {running ? "Running…" : "Run all 5 steps"}
        </button>
        {mode === "app" ? (
          <label className="runner-toggle">
            <input type="checkbox" checked={includeToken} onChange={(event) => setIncludeToken(event.target.checked)} />
            <span>Include my live token in copied snippets</span>
          </label>
        ) : null}
      </div>

      <ol className="runner-steps">
        {STEP_META.map((meta) => {
          const state = steps[meta.id]
          const tab = snippetTab[meta.id]
          const teaching = meta.id === "viewPublic" && state.status === "error" && state.httpStatus === 404 && visibility === "private"
          return (
            <li key={meta.id} className="runner-step" data-status={state.status}>
              <div className="runner-step-head">
                <div className="runner-step-title">
                  <span className="runner-method">{resolveStep(meta.id, context).method}</span>
                  <strong>{meta.title}</strong>
                  <code>{resolveStep(meta.id, context).path}</code>
                </div>
                <div className="runner-step-right">
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
                  <pre>{JSON.stringify(state.response, null, 2)}</pre>
                </details>
              ) : null}

              <div className="runner-snippet">
                <div className="runner-tabs">
                  <button type="button" className={tab === "curl" ? "active" : ""} onClick={() => setSnippetTab((prev) => ({ ...prev, [meta.id]: "curl" }))}>cURL</button>
                  <button type="button" className={tab === "fetch" ? "active" : ""} onClick={() => setSnippetTab((prev) => ({ ...prev, [meta.id]: "fetch" }))}>fetch</button>
                </div>
                <pre className="runner-code">{tab === "curl" ? buildCurl(meta.id, context, { maskToken }) : buildFetch(meta.id, context)}</pre>
              </div>
            </li>
          )
        })}
      </ol>

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

// The Operator Console is the deployment control plane for the super-operator
// bootstrapped from ADMIN_EMAIL / ADMIN_PASSWORD. It is lazy-loaded so its weight
// never lands in the public bundle, and every call goes through the operator-only
// /v1/admin endpoints. Nothing here ever shows a secret, password hash, or the
// raw env — only whether each is configured.

import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  Boxes,
  Clock,
  Gauge,
  LoaderCircle,
  LogOut,
  Plus,
  Power,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  Send,
  Server,
  ShieldCheck,
  Sparkles,
  Trash2,
  Users,
  Zap,
} from "lucide-react"
import { APIError, api, normalizeSameOriginUrl } from "../api"
import type {
  AdminAppPatch,
  AdminAppView,
  AdminBuilderView,
  AdminOverview,
  AdminSettings,
  AdminSettingsPatch,
  OutboxEvent,
  ResolvedSettings,
} from "../api"
import { useToast } from "../console/toastContext"
import { ConfirmButton } from "../console/ConfirmButton"
import { AppInspector } from "../console/AppInspector"
import { Highlighted } from "../runner/highlight"
import "./admin.css"

const ApiLoopRunner = lazy(() =>
  import("../runner/ApiLoopRunner").then((module) => ({ default: module.ApiLoopRunner })),
)

type TabId = "pulse" | "fleet" | "builders" | "outbox" | "tinker"

const TABS: { id: TabId; label: string; icon: typeof Activity }[] = [
  { id: "pulse", label: "Pulse & Controls", icon: Gauge },
  { id: "fleet", label: "Fleet", icon: Boxes },
  { id: "builders", label: "Builders", icon: Users },
  { id: "outbox", label: "Outbox & Traffic", icon: Send },
  { id: "tinker", label: "Tinker", icon: Zap },
]

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof APIError ? error.message : error instanceof Error ? error.message : fallback
}

const numberFormat = new Intl.NumberFormat("en")
function fmtNum(value: number | undefined): string {
  return numberFormat.format(value ?? 0)
}

function fmtDate(value?: string): string {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("en", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }).format(date)
}

function fmtUptime(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds))
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

export default function OperatorConsole({
  email,
  selfId,
  onSignedOut,
  onBackToConsole,
}: {
  email: string
  selfId: string
  onSignedOut: () => void
  onBackToConsole: () => void
}) {
  const [tab, setTab] = useState<TabId>("pulse")

  async function signOut() {
    try {
      await api.builderLogout()
    } finally {
      onSignedOut()
    }
  }

  return (
    <main className="op-console">
      <header className="op-head">
        <div className="op-head-id">
          <p className="eyebrow">
            <ShieldCheck aria-hidden="true" size={15} /> Deployment admin
          </p>
          <h1>Operator control plane</h1>
          <p className="op-id">
            Signed in as {email} <span className="op-badge">operator</span>
          </p>
        </div>
        <div className="op-head-actions">
          <button className="text-button" onClick={onBackToConsole} type="button">
            <ArrowLeft aria-hidden="true" size={15} /> Builder console
          </button>
          <button className="text-button" onClick={() => void signOut()} type="button">
            <LogOut aria-hidden="true" size={15} /> Sign out
          </button>
        </div>
      </header>

      <nav className="op-tabs" aria-label="Operator sections">
        {TABS.map((item) => {
          const Icon = item.icon
          return (
            <button
              key={item.id}
              type="button"
              className={tab === item.id ? "active" : ""}
              aria-current={tab === item.id}
              onClick={() => setTab(item.id)}
            >
              <Icon aria-hidden="true" size={15} /> {item.label}
            </button>
          )
        })}
      </nav>

      <div className="op-panel">
        {tab === "pulse" && <PulsePanel />}
        {tab === "fleet" && <FleetPanel />}
        {tab === "builders" && <BuildersPanel selfId={selfId} />}
        {tab === "outbox" && <OutboxPanel />}
        {tab === "tinker" && <TinkerPanel />}
      </div>
    </main>
  )
}

// --------------------------------------------------------------------------
// Pulse & Live Controls
// --------------------------------------------------------------------------

type SettingsDraft = {
  trySandbox: boolean
  builderSignupOpen: boolean
  demoSeed: boolean
  webhookDelivery: boolean
  trySandboxTTL: string
  vibeShareTTL: string
  builderSignupDailyLimit: number
  freeTierMaxApps: number
  freeTierMaxUsers: number
  freeTierMaxRecords: number
  freeTierMaxRecordBytes: number
  vibeMaxSandboxes: number
  vibeDailyLimit: number
}

const BOOLEAN_KEYS = ["trySandbox", "builderSignupOpen", "demoSeed", "webhookDelivery"] as const
const DURATION_KEYS = ["trySandboxTTL", "vibeShareTTL"] as const
const NUMBER_KEYS = [
  "builderSignupDailyLimit",
  "freeTierMaxApps",
  "freeTierMaxUsers",
  "freeTierMaxRecords",
  "freeTierMaxRecordBytes",
  "vibeMaxSandboxes",
  "vibeDailyLimit",
] as const

function draftFrom(effective: ResolvedSettings): SettingsDraft {
  return {
    trySandbox: effective.trySandbox,
    builderSignupOpen: effective.builderSignupOpen,
    demoSeed: effective.demoSeed,
    webhookDelivery: effective.webhookDelivery,
    trySandboxTTL: effective.trySandboxTTL,
    vibeShareTTL: effective.vibeShareTTL,
    builderSignupDailyLimit: effective.builderSignupDailyLimit,
    freeTierMaxApps: effective.freeTierMaxApps,
    freeTierMaxUsers: effective.freeTierMaxUsers,
    freeTierMaxRecords: effective.freeTierMaxRecords,
    freeTierMaxRecordBytes: effective.freeTierMaxRecordBytes,
    vibeMaxSandboxes: effective.vibeMaxSandboxes,
    vibeDailyLimit: effective.vibeDailyLimit,
  }
}

const COUNT_LABELS: { key: string; label: string; icon: typeof Activity }[] = [
  { key: "liveApps", label: "Live apps", icon: Boxes },
  { key: "sandboxes", label: "Sandboxes", icon: Zap },
  { key: "builders", label: "Builders", icon: Users },
  { key: "operators", label: "Operators", icon: ShieldCheck },
  { key: "endUsers", label: "End users", icon: Users },
  { key: "records", label: "Records", icon: Server },
  { key: "sessions", label: "Sessions", icon: Activity },
  { key: "webhooks", label: "Webhooks", icon: Send },
]

function PulsePanel() {
  const toast = useToast()
  const [overview, setOverview] = useState<AdminOverview>()
  const [settings, setSettings] = useState<AdminSettings>()
  const [draft, setDraft] = useState<SettingsDraft>()
  const [loadError, setLoadError] = useState("")
  const [saving, setSaving] = useState(false)

  const loadOverview = useCallback(async () => {
    try {
      setOverview(await api.adminOverview())
    } catch (error) {
      setLoadError(errorMessage(error, "Could not load the overview."))
    }
  }, [])

  const loadSettings = useCallback(async () => {
    try {
      const next = await api.adminGetSettings()
      setSettings(next)
      setDraft(draftFrom(next.effective))
    } catch (error) {
      setLoadError(errorMessage(error, "Could not load settings."))
    }
  }, [])

  useEffect(() => {
    // Defer the first fetch to a macrotask so the effect never calls setState
    // synchronously, then poll the live pulse on an interval.
    const initial = window.setTimeout(() => {
      void loadOverview()
      void loadSettings()
    }, 0)
    const handle = window.setInterval(() => void loadOverview(), 5000)
    return () => {
      window.clearTimeout(initial)
      window.clearInterval(handle)
    }
  }, [loadOverview, loadSettings])

  const overriddenKeys = useMemo(
    () => new Set(settings ? Object.keys(settings.overrides) : []),
    [settings],
  )

  async function save() {
    if (!settings || !draft) return
    const base = settings.effective
    const patch: AdminSettingsPatch = {}
    for (const key of BOOLEAN_KEYS) if (draft[key] !== base[key]) patch[key] = draft[key]
    for (const key of DURATION_KEYS) if (draft[key].trim() !== base[key]) patch[key] = draft[key].trim()
    for (const key of NUMBER_KEYS) if (draft[key] !== base[key]) patch[key] = draft[key]
    if (Object.keys(patch).length === 0) {
      toast.success("No changes to save")
      return
    }
    setSaving(true)
    try {
      const next = await api.adminPatchSettings(patch)
      setSettings(next)
      setDraft(draftFrom(next.effective))
      toast.success("Settings applied live")
      void loadOverview()
    } catch (error) {
      toast.error(errorMessage(error, "Could not apply settings."))
    } finally {
      setSaving(false)
    }
  }

  async function resetField(key: string) {
    setSaving(true)
    try {
      const next = await api.adminPatchSettings({ resetKeys: [key] })
      setSettings(next)
      setDraft(draftFrom(next.effective))
      toast.success("Reset to the .env default")
    } catch (error) {
      toast.error(errorMessage(error, "Could not reset the setting."))
    } finally {
      setSaving(false)
    }
  }

  async function resetAll() {
    setSaving(true)
    try {
      const next = await api.adminPatchSettings({ resetAll: true })
      setSettings(next)
      setDraft(draftFrom(next.effective))
      toast.success("All overrides cleared")
      void loadOverview()
    } catch (error) {
      toast.error(errorMessage(error, "Could not reset overrides."))
    } finally {
      setSaving(false)
    }
  }

  async function clearVibeCooldown() {
    try {
      await api.adminClearVibeCooldown()
      toast.success("Vibe cooldown cleared")
      void loadSettings()
    } catch (error) {
      toast.error(errorMessage(error, "Could not clear the cooldown."))
    }
  }

  if (loadError && !overview && !settings) {
    return <p className="op-state error">{loadError}</p>
  }
  if (!overview || !settings || !draft) {
    return (
      <p className="op-state">
        <LoaderCircle aria-hidden="true" className="spin" size={16} /> Reading the deployment…
      </p>
    )
  }

  const metrics = overview.metrics
  const statusClasses = metrics.statusClasses ?? {}
  const topRoutes = metrics.routes.slice(0, 8)
  const env = settings.environment

  function boolRow(key: (typeof BOOLEAN_KEYS)[number], label: string, hint: string) {
    return (
      <div className="op-control">
        <label className="op-switch">
          <input
            type="checkbox"
            checked={draft![key]}
            onChange={(event) => setDraft({ ...draft!, [key]: event.target.checked })}
          />
          <span className="op-switch-track" aria-hidden="true" />
          <span className="op-control-text">
            <strong>{label}</strong>
            <small>{hint}</small>
          </span>
        </label>
        {overriddenKeys.has(key) && (
          <button type="button" className="op-reset" onClick={() => void resetField(key)}>
            <RotateCcw aria-hidden="true" size={12} /> env
          </button>
        )}
      </div>
    )
  }

  function numberRow(key: (typeof NUMBER_KEYS)[number], label: string, hint: string) {
    return (
      <div className="op-control">
        <label className="op-field">
          <span className="op-control-text">
            <strong>{label}</strong>
            <small>{hint}</small>
          </span>
          <input
            type="number"
            min={0}
            value={String(draft![key])}
            onChange={(event) => setDraft({ ...draft!, [key]: Math.max(0, Math.floor(Number(event.target.value) || 0)) })}
          />
        </label>
        {overriddenKeys.has(key) && (
          <button type="button" className="op-reset" onClick={() => void resetField(key)}>
            <RotateCcw aria-hidden="true" size={12} /> env
          </button>
        )}
      </div>
    )
  }

  function durationRow(key: (typeof DURATION_KEYS)[number], label: string, hint: string) {
    return (
      <div className="op-control">
        <label className="op-field">
          <span className="op-control-text">
            <strong>{label}</strong>
            <small>{hint}</small>
          </span>
          <input
            type="text"
            spellCheck={false}
            value={draft![key]}
            placeholder="24h"
            onChange={(event) => setDraft({ ...draft!, [key]: event.target.value })}
          />
        </label>
        {overriddenKeys.has(key) && (
          <button type="button" className="op-reset" onClick={() => void resetField(key)}>
            <RotateCcw aria-hidden="true" size={12} /> env
          </button>
        )}
      </div>
    )
  }

  return (
    <div className="op-pulse">
      <section className="op-section">
        <div className="op-section-head">
          <h2>
            <Activity aria-hidden="true" size={16} /> Live pulse
          </h2>
          <button className="icon-button" aria-label="Refresh" title="Refresh" onClick={() => void loadOverview()}>
            <RefreshCw aria-hidden="true" size={15} />
          </button>
        </div>
        <div className="op-stat-grid">
          {COUNT_LABELS.map((item) => {
            const Icon = item.icon
            return (
              <div className="op-stat" key={item.key}>
                <Icon aria-hidden="true" size={16} />
                <span className="op-stat-value">{fmtNum(overview.counts[item.key])}</span>
                <span className="op-stat-label">{item.label}</span>
              </div>
            )
          })}
        </div>
      </section>

      <section className="op-section">
        <h2>
          <Gauge aria-hidden="true" size={16} /> Traffic
        </h2>
        <div className="op-metric-row">
          <div className="op-metric">
            <span className="op-metric-value">{fmtNum(metrics.totalRequests)}</span>
            <span className="op-metric-label">requests</span>
          </div>
          <div className="op-metric">
            <span className="op-metric-value">{metrics.avgLatencyMs.toFixed(1)}ms</span>
            <span className="op-metric-label">avg latency</span>
          </div>
          <div className="op-metric">
            <span className="op-metric-value">{fmtNum(metrics.inFlight)}</span>
            <span className="op-metric-label">in flight</span>
          </div>
          <div className="op-metric">
            <span className="op-metric-value">{fmtUptime(metrics.uptimeSeconds)}</span>
            <span className="op-metric-label">uptime</span>
          </div>
          <div className="op-metric">
            <span className="op-metric-value">{fmtNum(metrics.rateLimited)}</span>
            <span className="op-metric-label">rate limited</span>
          </div>
        </div>
        <div className="op-status-pills">
          {["2xx", "3xx", "4xx", "5xx"].map((cls) => (
            <span key={cls} className={`op-pill op-pill-${cls}`}>
              {cls}: {fmtNum(statusClasses[cls])}
            </span>
          ))}
        </div>
        {topRoutes.length > 0 && (
          <table className="op-table">
            <thead>
              <tr>
                <th>Route</th>
                <th>Status</th>
                <th>Count</th>
                <th>Avg</th>
              </tr>
            </thead>
            <tbody>
              {topRoutes.map((route) => (
                <tr key={`${route.method}-${route.route}-${route.status}`}>
                  <td>
                    <code>
                      {route.method} {route.route}
                    </code>
                  </td>
                  <td>{route.status}</td>
                  <td>{fmtNum(route.count)}</td>
                  <td>{route.avgLatencyMs.toFixed(1)}ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="op-section">
        <div className="op-section-head">
          <h2>
            <Power aria-hidden="true" size={16} /> Live controls
          </h2>
          <ConfirmButton confirmLabel="Reset all to .env" onConfirm={() => void resetAll()} disabled={saving || overriddenKeys.size === 0}>
            <RotateCcw aria-hidden="true" size={14} /> Reset all overrides
          </ConfirmButton>
        </div>
        <p className="op-note">
          Changes take effect across the deployment within seconds — no restart. A field marked <span className="op-badge-mini">env</span> can be
          reset to its <code>.env</code> baseline.
        </p>

        <div className="op-controls-grid">
          {boolRow("trySandbox", "Try-it sandbox", "Allow the public /v1/try blank sandbox and unlock instant Vibe recipes")}
          {boolRow("builderSignupOpen", "Builder signup", "Allow self-serve builder accounts")}
          {boolRow("demoSeed", "Demo data", "Seed the storytelling demo on the default app")}
          {boolRow("webhookDelivery", "Webhook delivery", "Run the outbox delivery worker")}
          {durationRow("trySandboxTTL", "Try-it TTL", "Lifetime of a try-it sandbox, e.g. 24h")}
          {durationRow("vibeShareTTL", "Vibe share TTL", "Lifetime of a vibe sandbox, e.g. 72h")}
          {numberRow("builderSignupDailyLimit", "Signup / day", "0 = unlimited")}
          {numberRow("vibeDailyLimit", "Vibe builds / day", "0 = unlimited")}
          {numberRow("vibeMaxSandboxes", "Max sandboxes", "Global live sandbox cap")}
          {numberRow("freeTierMaxApps", "Free apps / builder", "0 = unlimited")}
          {numberRow("freeTierMaxUsers", "Free tier users", "Per app, 0 = unlimited")}
          {numberRow("freeTierMaxRecords", "Free tier records", "Per namespace, 0 = unlimited")}
          {numberRow("freeTierMaxRecordBytes", "Free record bytes", "Per record, 0 = unlimited")}
        </div>

        <div className="op-actions">
          <button className="button primary" disabled={saving} onClick={() => void save()} type="button">
            {saving ? <LoaderCircle aria-hidden="true" className="spin" size={16} /> : <Save aria-hidden="true" size={16} />} Apply changes
          </button>
        </div>
      </section>

      <section className="op-section">
        <h2>
          <ShieldCheck aria-hidden="true" size={16} /> Environment posture
        </h2>
        <div className="op-posture">
          <PostureItem label="Gemini (vibe)" on={env.geminiConfigured} detail={env.geminiConfigured ? env.geminiModel : "not configured"} />
          <PostureItem label="Metrics token" on={env.metricsTokenConfigured} />
          <PostureItem label="Platform token" on={env.platformTokenConfigured} />
          <PostureItem label="Trust proxy" on={env.trustProxy} />
          <PostureItem label="Secure cookies" on={env.sessionCookieSecure} />
          <PostureItem label="Webhook worker" on={env.webhookRunning} detail={env.webhookRunning ? "running" : "stopped"} />
        </div>
        <dl className="op-posture-meta">
          <div>
            <dt>Database</dt>
            <dd>
              <code>{env.database || "—"}</code>
            </dd>
          </div>
          <div>
            <dt>Public base URL</dt>
            <dd>
              <code>{env.publicBaseURL || "(request origin)"}</code>
            </dd>
          </div>
          <div>
            <dt>Allowed origins</dt>
            <dd>
              <code>{env.allowedOrigins.length ? env.allowedOrigins.join(", ") : "(none)"}</code>
            </dd>
          </div>
        </dl>
        {env.vibeCoolingDown && (
          <div className="op-cooldown">
            <AlertTriangle aria-hidden="true" size={15} /> The vibe model is in a cooldown after a provider failure.
            <button type="button" className="button secondary small-button" onClick={() => void clearVibeCooldown()}>
              <Zap aria-hidden="true" size={13} /> Clear cooldown
            </button>
          </div>
        )}
      </section>
    </div>
  )
}

function PostureItem({ label, on, detail }: { label: string; on: boolean; detail?: string }) {
  return (
    <div className={`op-posture-item ${on ? "on" : "off"}`}>
      <span className="op-posture-dot" aria-hidden="true" />
      <span className="op-posture-label">{label}</span>
      <span className="op-posture-detail">{detail ?? (on ? "configured" : "not configured")}</span>
    </div>
  )
}

// --------------------------------------------------------------------------
// Fleet
// --------------------------------------------------------------------------

const FLEET_FILTERS: { id: string; label: string }[] = [
  { id: "all", label: "All" },
  { id: "live", label: "Live" },
  { id: "sandbox", label: "Sandboxes" },
  { id: "disabled", label: "Disabled" },
]

function FleetPanel() {
  const toast = useToast()
  const [apps, setApps] = useState<AdminAppView[]>()
  const [filter, setFilter] = useState("all")
  const [query, setQuery] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [sweeping, setSweeping] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setApps(await api.adminListApps({ filter: filter === "all" ? undefined : filter, q: query.trim() || undefined }))
    } catch (requestError) {
      setError(errorMessage(requestError, "Could not load applications."))
    } finally {
      setLoading(false)
    }
  }, [filter, query])

  useEffect(() => {
    const handle = window.setTimeout(() => void load(), 220)
    return () => window.clearTimeout(handle)
  }, [load])

  async function sweep() {
    setSweeping(true)
    try {
      const result = await api.adminSweepSandboxes()
      toast.success(`Swept ${result.purged} expired sandbox${result.purged === 1 ? "" : "es"}`)
      void load()
    } catch (requestError) {
      toast.error(errorMessage(requestError, "Could not sweep sandboxes."))
    } finally {
      setSweeping(false)
    }
  }

  function onUpdated(updated: AdminAppView) {
    setApps((current) => (current ?? []).map((item) => (item.id === updated.id ? updated : item)))
  }
  function onDeleted(id: string) {
    setApps((current) => (current ?? []).filter((item) => item.id !== id))
  }

  return (
    <div className="op-fleet">
      <div className="op-fleet-bar">
        <div className="op-segmented" role="tablist" aria-label="Filter apps">
          {FLEET_FILTERS.map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              aria-selected={filter === item.id}
              className={filter === item.id ? "active" : ""}
              onClick={() => setFilter(item.id)}
            >
              {item.label}
            </button>
          ))}
        </div>
        <label className="op-search">
          <Search aria-hidden="true" size={15} />
          <input
            value={query}
            placeholder="Search slug, name, or key"
            spellCheck={false}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <ConfirmButton confirmLabel="Sweep expired" onConfirm={() => void sweep()} disabled={sweeping}>
          <Trash2 aria-hidden="true" size={14} /> Sweep sandboxes
        </ConfirmButton>
      </div>

      {error && <p className="op-state error">{error}</p>}
      {loading && !apps ? (
        <p className="op-state">
          <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading fleet…
        </p>
      ) : apps && apps.length > 0 ? (
        <ul className="op-app-list">
          {apps.map((item) => (
            <FleetAppRow key={item.id} app={item} onUpdated={onUpdated} onDeleted={onDeleted} />
          ))}
        </ul>
      ) : (
        <p className="op-state">No applications match.</p>
      )}
    </div>
  )
}

function FleetAppRow({
  app,
  onUpdated,
  onDeleted,
}: {
  app: AdminAppView
  onUpdated: (updated: AdminAppView) => void
  onDeleted: (id: string) => void
}) {
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [inspecting, setInspecting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [limits, setLimits] = useState({
    maxUsers: app.limits.maxUsers,
    maxRecordsPerNamespace: app.limits.maxRecordsPerNamespace,
    maxRecordBytes: app.limits.maxRecordBytes,
  })
  const [ownerId, setOwnerId] = useState("")

  async function patch(update: AdminAppPatch, successMessage: string) {
    setBusy(true)
    try {
      const updated = await api.adminUpdateApp(app.id, update)
      onUpdated(updated)
      setLimits({
        maxUsers: updated.limits.maxUsers,
        maxRecordsPerNamespace: updated.limits.maxRecordsPerNamespace,
        maxRecordBytes: updated.limits.maxRecordBytes,
      })
      toast.success(successMessage)
    } catch (error) {
      toast.error(errorMessage(error, "Could not update the app."))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    try {
      await api.adminDeleteApp(app.id)
      toast.success("Application deleted")
      onDeleted(app.id)
    } catch (error) {
      toast.error(errorMessage(error, "Could not delete the app."))
      setBusy(false)
    }
  }

  return (
    <li className={`op-app ${open ? "open" : ""}`}>
      <div className="op-app-row">
        <button type="button" className="op-app-main" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="op-app-title">
            {app.displayName}
            {app.sandbox && <span className="op-tag sandbox">sandbox</span>}
            {!app.active && <span className="op-tag off">disabled</span>}
          </span>
          <span className="op-app-meta">
            <code>{app.slug}</code>
            <span>
              {fmtNum(app.users)} users · {fmtNum(app.records)} records
            </span>
            {app.ownerEmail ? <span className="op-owner">{app.ownerEmail}</span> : <span className="op-owner none">unowned</span>}
          </span>
        </button>
        <div className="op-app-quick">
          <button
            type="button"
            className="button secondary small-button"
            disabled={busy}
            onClick={() => void patch({ active: !app.active }, app.active ? "App disabled" : "App enabled")}
          >
            <Power aria-hidden="true" size={13} /> {app.active ? "Disable" : "Enable"}
          </button>
          <ConfirmButton variant="icon" confirmLabel="Delete app" ariaLabel={`Delete ${app.displayName}`} onConfirm={() => void remove()} disabled={busy} />
        </div>
      </div>

      {open && (
        <div className="op-app-detail">
          <div className="op-kv">
            <span>Publishable key</span>
            <code>{app.publishableKey}</code>
          </div>
          <div className="op-kv">
            <span>Created</span>
            <span>{fmtDate(app.createdAt)}</span>
          </div>
          {app.sandbox && (
            <div className="op-kv">
              <span>Expires</span>
              <span>{fmtDate(app.expiresAt)}</span>
            </div>
          )}

          {app.sandbox && (
            <div className="op-detail-actions">
              <button type="button" className="button secondary small-button" disabled={busy} onClick={() => void patch({ extendHours: 24 }, "Extended by 24h")}>
                <Clock aria-hidden="true" size={13} /> Extend 24h
              </button>
              <ConfirmButton confirmLabel="Promote to live" onConfirm={() => void patch({ promote: true }, "Promoted to a live app")} disabled={busy}>
                <Sparkles aria-hidden="true" size={13} /> Promote to live
              </ConfirmButton>
            </div>
          )}

          <div className="op-limits">
            <h4>Quotas (0 = unlimited)</h4>
            <div className="op-limits-grid">
              <label>
                Users
                <input type="number" min={0} value={String(limits.maxUsers)} onChange={(event) => setLimits({ ...limits, maxUsers: Math.max(0, Math.floor(Number(event.target.value) || 0)) })} />
              </label>
              <label>
                Records / namespace
                <input type="number" min={0} value={String(limits.maxRecordsPerNamespace)} onChange={(event) => setLimits({ ...limits, maxRecordsPerNamespace: Math.max(0, Math.floor(Number(event.target.value) || 0)) })} />
              </label>
              <label>
                Bytes / record
                <input type="number" min={0} value={String(limits.maxRecordBytes)} onChange={(event) => setLimits({ ...limits, maxRecordBytes: Math.max(0, Math.floor(Number(event.target.value) || 0)) })} />
              </label>
            </div>
            <div className="op-detail-actions">
              <button type="button" className="button secondary small-button" disabled={busy} onClick={() => void patch({ limits }, "Quotas updated")}>
                <Save aria-hidden="true" size={13} /> Save quotas
              </button>
              <button type="button" className="button secondary small-button" disabled={busy} onClick={() => void patch({ unlimited: true }, "App set to unlimited")}>
                Make unlimited
              </button>
            </div>
          </div>

          <div className="op-reassign">
            <h4>Reassign owner</h4>
            <div className="op-detail-actions">
              <input
                type="text"
                spellCheck={false}
                placeholder="builder or operator id"
                value={ownerId}
                onChange={(event) => setOwnerId(event.target.value)}
              />
              <button type="button" className="button secondary small-button" disabled={busy || !ownerId.trim()} onClick={() => void patch({ ownerId: ownerId.trim() }, "Owner reassigned")}>
                Reassign
              </button>
            </div>
          </div>

          <div className="op-detail-actions">
            <button type="button" className="button secondary small-button" onClick={() => setInspecting(!inspecting)}>
              <Server aria-hidden="true" size={13} /> {inspecting ? "Hide data" : "Inspect data"}
            </button>
          </div>
          {inspecting && <AppInspector appId={app.id} appName={app.displayName} scope="admin" />}
        </div>
      )}
    </li>
  )
}

// --------------------------------------------------------------------------
// Builders
// --------------------------------------------------------------------------

function BuildersPanel({ selfId }: { selfId: string }) {
  const toast = useToast()
  const [builders, setBuilders] = useState<AdminBuilderView[]>()
  const [error, setError] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState<"builder" | "operator">("builder")
  const [plan, setPlan] = useState<"free" | "unlimited">("unlimited")
  const [creating, setCreating] = useState(false)

  const load = useCallback(async () => {
    setError("")
    try {
      setBuilders(await api.adminListBuilders())
    } catch (requestError) {
      setError(errorMessage(requestError, "Could not load builders."))
    }
  }, [])

  useEffect(() => {
    const handle = window.setTimeout(() => void load(), 0)
    return () => window.clearTimeout(handle)
  }, [load])

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setCreating(true)
    try {
      const created = await api.adminCreateBuilder({ email, password, role, plan })
      setBuilders((current) => [created, ...(current ?? [])])
      setEmail("")
      setPassword("")
      toast.success(`${role === "operator" ? "Operator" : "Builder"} created`)
    } catch (requestError) {
      toast.error(errorMessage(requestError, "Could not create the account."))
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="op-builders">
      <section className="op-section">
        <h2>
          <Plus aria-hidden="true" size={16} /> Create account
        </h2>
        <form className="op-create-form" onSubmit={(event) => void create(event)}>
          <div className="op-create-grid">
            <label>
              Email
              <input type="email" autoComplete="off" required value={email} onChange={(event) => setEmail(event.target.value)} />
            </label>
            <label>
              Password
              <input type="password" autoComplete="new-password" minLength={12} required placeholder="at least 12 characters" value={password} onChange={(event) => setPassword(event.target.value)} />
            </label>
            <label>
              Role
              <select value={role} onChange={(event) => setRole(event.target.value as "builder" | "operator")}>
                <option value="builder">Builder</option>
                <option value="operator">Operator</option>
              </select>
            </label>
            <label>
              Plan
              <select value={plan} onChange={(event) => setPlan(event.target.value as "free" | "unlimited")}>
                <option value="unlimited">Unlimited</option>
                <option value="free">Free tier</option>
              </select>
            </label>
          </div>
          <div className="op-actions">
            <button className="button primary" type="submit" disabled={creating || !email || password.length < 12}>
              {creating ? <LoaderCircle aria-hidden="true" className="spin" size={16} /> : <Plus aria-hidden="true" size={16} />} Create account
            </button>
          </div>
        </form>
      </section>

      <section className="op-section">
        <div className="op-section-head">
          <h2>
            <Users aria-hidden="true" size={16} /> Control-plane accounts
          </h2>
          <button className="icon-button" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
            <RefreshCw aria-hidden="true" size={15} />
          </button>
        </div>
        {error && <p className="op-state error">{error}</p>}
        {!builders ? (
          <p className="op-state">
            <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading…
          </p>
        ) : builders.length === 0 ? (
          <p className="op-state">No builders yet.</p>
        ) : (
          <ul className="op-builder-list">
            {builders.map((builder) => (
              <BuilderRow
                key={builder.id}
                builder={builder}
                isSelf={builder.id === selfId}
                onChanged={load}
                onRemoved={(id) => setBuilders((current) => (current ?? []).filter((item) => item.id !== id))}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function BuilderRow({
  builder,
  isSelf,
  onChanged,
  onRemoved,
}: {
  builder: AdminBuilderView
  isSelf: boolean
  onChanged: () => void
  onRemoved: (id: string) => void
}) {
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [password, setPassword] = useState("")
  const locked = builder.isBootstrapAdmin || isSelf

  async function update(patch: Parameters<typeof api.adminUpdateBuilder>[1], message: string) {
    setBusy(true)
    try {
      await api.adminUpdateBuilder(builder.id, patch)
      toast.success(message)
      setPassword("")
      onChanged()
    } catch (error) {
      toast.error(errorMessage(error, "Could not update the account."))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    try {
      await api.adminDeleteBuilder(builder.id)
      toast.success("Account and its apps deleted")
      onRemoved(builder.id)
    } catch (error) {
      toast.error(errorMessage(error, "Could not delete the account."))
      setBusy(false)
    }
  }

  return (
    <li className={`op-builder ${open ? "open" : ""}`}>
      <div className="op-builder-row">
        <button type="button" className="op-builder-main" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="op-builder-title">
            {builder.email}
            <span className={`op-tag role-${builder.role}`}>{builder.role}</span>
            {builder.isBootstrapAdmin && <span className="op-tag bootstrap">.env</span>}
            {builder.plan === "free" && <span className="op-tag free">free</span>}
          </span>
          <span className="op-builder-meta">
            {fmtNum(builder.appCount)} app{builder.appCount === 1 ? "" : "s"} · joined {fmtDate(builder.createdAt)}
          </span>
        </button>
        <ConfirmButton
          variant="icon"
          confirmLabel="Delete account"
          ariaLabel={`Delete ${builder.email}`}
          onConfirm={() => void remove()}
          disabled={busy || locked}
          title={locked ? "The bootstrap operator and your own account are protected" : undefined}
        />
      </div>

      {open && (
        <div className="op-builder-detail">
          {locked && (
            <p className="op-note">
              <ShieldCheck aria-hidden="true" size={13} />{" "}
              {builder.isBootstrapAdmin ? "This is the .env bootstrap operator — role is locked." : "This is your own account."}
            </p>
          )}
          <div className="op-detail-actions">
            <button
              type="button"
              className="button secondary small-button"
              disabled={busy || builder.plan === "free"}
              onClick={() => void update({ plan: "free", applyLimitsToApps: true }, "Moved to free tier")}
            >
              Free tier
            </button>
            <button
              type="button"
              className="button secondary small-button"
              disabled={busy || !builder.plan}
              onClick={() => void update({ plan: "unlimited", applyLimitsToApps: true }, "Set to unlimited")}
            >
              Unlimited
            </button>
            {builder.role === "builder" ? (
              <button type="button" className="button secondary small-button" disabled={busy} onClick={() => void update({ role: "operator" }, "Promoted to operator")}>
                <ShieldCheck aria-hidden="true" size={13} /> Make operator
              </button>
            ) : (
              <button type="button" className="button secondary small-button" disabled={busy || locked} onClick={() => void update({ role: "builder" }, "Demoted to builder")}>
                Demote to builder
              </button>
            )}
          </div>
          <div className="op-reassign">
            <h4>Reset password</h4>
            <div className="op-detail-actions">
              <input type="password" autoComplete="new-password" minLength={12} placeholder="new password (12+ chars)" value={password} onChange={(event) => setPassword(event.target.value)} />
              <button type="button" className="button secondary small-button" disabled={busy || password.length < 12} onClick={() => void update({ password }, "Password reset; sessions revoked")}>
                Reset password
              </button>
            </div>
          </div>
        </div>
      )}
    </li>
  )
}

// --------------------------------------------------------------------------
// Outbox & Traffic
// --------------------------------------------------------------------------

const OUTBOX_STATES = ["", "pending", "retry", "sending", "delivered", "dead"]

function OutboxPanel() {
  const toast = useToast()
  const [events, setEvents] = useState<OutboxEvent[]>()
  const [state, setState] = useState("")
  const [error, setError] = useState("")
  const [expanded, setExpanded] = useState<string>()
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setError("")
    try {
      setEvents(await api.adminListOutbox(state || undefined))
    } catch (requestError) {
      setError(errorMessage(requestError, "Could not load outbox events."))
    }
  }, [state])

  useEffect(() => {
    const handle = window.setTimeout(() => void load(), 0)
    return () => window.clearTimeout(handle)
  }, [load])

  async function retry(id: string) {
    try {
      await api.adminRetryOutbox(id)
      toast.success("Event queued for redelivery")
      void load()
    } catch (requestError) {
      toast.error(errorMessage(requestError, "Could not retry the event."))
    }
  }

  async function retryDead() {
    setBusy(true)
    try {
      const result = await api.adminRetryDeadOutbox()
      toast.success(`Requeued ${result.retried} dead event${result.retried === 1 ? "" : "s"}`)
      void load()
    } catch (requestError) {
      toast.error(errorMessage(requestError, "Could not requeue dead events."))
    } finally {
      setBusy(false)
    }
  }

  async function prune() {
    setBusy(true)
    try {
      const result = await api.adminPruneDelivered()
      toast.success(`Pruned ${result.pruned} delivered event${result.pruned === 1 ? "" : "s"}`)
      void load()
    } catch (requestError) {
      toast.error(errorMessage(requestError, "Could not prune delivered events."))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="op-outbox">
      <div className="op-fleet-bar">
        <label className="op-field inline">
          State
          <select value={state} onChange={(event) => setState(event.target.value)}>
            {OUTBOX_STATES.map((value) => (
              <option key={value || "all"} value={value}>
                {value || "All"}
              </option>
            ))}
          </select>
        </label>
        <button className="icon-button" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
          <RefreshCw aria-hidden="true" size={15} />
        </button>
        <div className="op-outbox-bulk">
          <ConfirmButton confirmLabel="Requeue dead" onConfirm={() => void retryDead()} disabled={busy}>
            <RefreshCw aria-hidden="true" size={13} /> Retry dead-lettered
          </ConfirmButton>
          <ConfirmButton confirmLabel="Prune delivered" onConfirm={() => void prune()} disabled={busy}>
            <Trash2 aria-hidden="true" size={13} /> Prune delivered
          </ConfirmButton>
        </div>
      </div>

      {error && <p className="op-state error">{error}</p>}
      {!events ? (
        <p className="op-state">
          <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading events…
        </p>
      ) : events.length === 0 ? (
        <p className="op-state">No events{state ? ` in state "${state}"` : ""}.</p>
      ) : (
        <ul className="op-event-list">
          {events.map((event) => {
            const isOpen = expanded === event.id
            return (
              <li key={event.id} className={isOpen ? "open" : ""}>
                <div className="op-event-row">
                  <button type="button" className="op-event-main" aria-expanded={isOpen} onClick={() => setExpanded(isOpen ? undefined : event.id)}>
                    <span className="op-event-type">{event.type}</span>
                    <span className="op-event-meta">
                      <span className={`op-tag state-${event.delivery.state}`}>{event.delivery.state}</span>
                      <span>attempt {event.delivery.attempts}</span>
                      <span>{fmtDate(event.occurredAt)}</span>
                    </span>
                  </button>
                  <button type="button" className="button secondary small-button" onClick={() => void retry(event.id)}>
                    <RefreshCw aria-hidden="true" size={13} /> Retry
                  </button>
                </div>
                {isOpen && (
                  <pre className="op-event-json">
                    <Highlighted code={JSON.stringify(event, null, 2)} lang="json" />
                  </pre>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

// --------------------------------------------------------------------------
// Tinker
// --------------------------------------------------------------------------

function TinkerPanel() {
  const ensureSandbox = useCallback(async () => {
    const result = await api.adminTinkerSandbox()
    return { pk: result.publishableKey, base: normalizeSameOriginUrl(result.apiBase), expiresAt: result.expiresAt }
  }, [])

  return (
    <div className="op-tinker">
      <section className="op-section">
        <h2>
          <Zap aria-hidden="true" size={16} /> Tinker workbench
        </h2>
        <p className="op-note">
          Spin up a throwaway sandbox tenant and drive the full API loop — register, log in, create, and read back public and private records —
          straight from here. The sandbox expires on its own and is swept automatically.
        </p>
        <Suspense
          fallback={
            <p className="op-state">
              <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading workbench…
            </p>
          }
        >
          <ApiLoopRunner mode="sandbox" appName="Operator tinker" onEnsureSandbox={ensureSandbox} />
        </Suspense>
      </section>
    </div>
  )
}

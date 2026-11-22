// The "Try it live" section mounts the full API loop runner immediately and
// provisions a disposable sandbox lazily, on the visitor's first run — so the
// landing page and the builder guide are always exactly one click away from
// watching five real, MongoDB-backed HTTP calls succeed. When TRY_SANDBOX is off
// it falls back to running the loop against a publishable key the visitor pastes
// from their console.

import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { ArrowUpRight, Sparkles } from "lucide-react"
import { APIError, api } from "../api"
import type { ServiceCapabilities, TrySandboxResult } from "../api"
import { ApiLoopRunner } from "./ApiLoopRunner"
import { saveClaimToken } from "./claimStorage"

export function TryItLiveSection() {
  const [capabilities, setCapabilities] = useState<ServiceCapabilities | null>(null)
  const [sandbox, setSandbox] = useState<TrySandboxResult | null>(null)
  const [pastedKey, setPastedKey] = useState("")
  const [manualKey, setManualKey] = useState("")

  useEffect(() => {
    let active = true
    api
      .getServiceManifest()
      .then((caps) => {
        if (active) setCapabilities(caps)
      })
      .catch(() => {
        if (active) setCapabilities({ trySandbox: false, builderSignup: false, operatorConfigured: false, vibeEnabled: false, vibeAIEnabled: false })
      })
    return () => {
      active = false
    }
  }, [])

  // Provisions a sandbox on the first run and remembers it (plus its one-time
  // claim token) so later runs and the "Keep this app" link reuse the same app.
  async function ensureSandbox() {
    if (sandbox) {
      return { pk: sandbox.publishableKey, base: sandbox.apiBase || window.location.origin, expiresAt: sandbox.expiresAt }
    }
    try {
      const result = await api.startTrySandbox()
      saveClaimToken(result.publishableKey, result.claim.token)
      setSandbox(result)
      return { pk: result.publishableKey, base: result.apiBase || window.location.origin, expiresAt: result.expiresAt }
    } catch (error) {
      if (error instanceof APIError && error.status === 503) {
        throw new Error("The playground is full right now. Please try again in a few minutes.")
      }
      throw new Error(error instanceof APIError ? error.message : "Could not start a sandbox right now.")
    }
  }

  return (
    <section className="guide-section try-live-section" id="try-live">
      <div className="guide-section-heading">
        <p className="eyebrow">
          <Sparkles aria-hidden="true" size={15} /> Try it live
        </p>
        <h2>Register, save, publish in 60 seconds.</h2>
        <p>
          Run the whole auth-to-public API loop against a real backend, right
          here — no install, no signup. Watch ownership get enforced on every
          request, then keep the app you just tried.
        </p>
      </div>

      {capabilities?.trySandbox ? (
        <ApiLoopRunner
          mode="sandbox"
          appName="your sandbox"
          onEnsureSandbox={ensureSandbox}
          expiresAt={sandbox?.expiresAt}
          onKeep={
            sandbox ? (
              <Link className="button primary" to={`/console?claim=${encodeURIComponent(sandbox.publishableKey)}`}>
                Keep this app <ArrowUpRight aria-hidden="true" size={16} />
              </Link>
            ) : undefined
          }
        />
      ) : capabilities ? (
        <div className="try-live-fallback">
          <p>
            Live try-it isn’t enabled on this deployment. Paste a publishable key
            from your <Link to="/console">console</Link> to run the same loop
            against your own app:
          </p>
          <div className="try-live-key">
            <input value={pastedKey} onChange={(event) => setPastedKey(event.target.value.trim())} placeholder="pk_…" aria-label="Publishable key" />
            <button type="button" className="button secondary" onClick={() => setManualKey(pastedKey)} disabled={!pastedKey.startsWith("pk_")}>
              Load runner
            </button>
          </div>
          {manualKey ? <ApiLoopRunner base={window.location.origin} pk={manualKey} mode="app" appName="your app" /> : null}
        </div>
      ) : (
        <p className="quiet">Checking availability…</p>
      )}
    </section>
  )
}

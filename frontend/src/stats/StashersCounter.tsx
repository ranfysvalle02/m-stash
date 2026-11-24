// StashersCounter shows the deployment's public, aggregate counts — building
// in the open. It renders nothing when stats are off, unavailable, or still
// zero, so a fresh self-hosted deployment never shows an empty scoreboard.

import { useEffect, useState } from "react"
import { api, type PublicStats } from "../api"

const COUNT_UP_MS = 900

function useCountUp(target: number): number {
  const [value, setValue] = useState(0)
  useEffect(() => {
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
    let frame = 0
    const start = performance.now()
    const tick = (now: number) => {
      const progress = reduced ? 1 : Math.min(1, (now - start) / COUNT_UP_MS)
      setValue(Math.round(target * (1 - Math.pow(1 - progress, 3))))
      if (progress < 1) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [target])
  return value
}

const format = (value: number) => value.toLocaleString()
const plural = (count: number, one: string, many: string) => (count === 1 ? one : many)

export default function StashersCounter({ compact = false }: { compact?: boolean }) {
  const [stats, setStats] = useState<PublicStats | null>(null)
  useEffect(() => {
    let active = true
    api
      .getPublicStats()
      .then((next) => {
        if (active) setStats(next)
      })
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [])
  const stashers = useCountUp(stats?.stashers ?? 0)
  if (!stats || stats.stashers === 0) return null

  return (
    <div className={`stash-stats${compact ? " compact" : ""}`}>
      <p className="stash-stats-headline">
        <span className="stash-stats-pulse" aria-hidden="true" />
        {/* The count-up is decorative; assistive tech reads the final value once. */}
        <strong aria-hidden="true">{format(stashers)}</strong>
        <span className="visually-hidden">{format(stats.stashers)}</span> registered{" "}
        {plural(stats.stashers, "stasher", "stashers")}
      </p>
      {!compact && (
        <ul className="stash-stats-list">
          <li>
            <strong>{format(stats.liveApps)}</strong> live {plural(stats.liveApps, "app", "apps")}
          </li>
          <li>
            <strong>{format(stats.records)}</strong> {plural(stats.records, "record", "records")} stashed in MongoDB
          </li>
          {stats.vibes30d > 0 && (
            <li>
              <strong>{format(stats.vibes30d)}</strong> {plural(stats.vibes30d, "app", "apps")} vibed in {stats.vibesWindowDays} days
            </li>
          )}
        </ul>
      )}
    </div>
  )
}

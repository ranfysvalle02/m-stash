import { useEffect, useState } from "react"

// useCountdown turns an ISO expiry timestamp into a live, human label such as
// "23h 59m left", re-rendering on an interval so a sandbox's remaining time
// ticks down in place. It returns "Expired" once the target passes and an empty
// string when there is no target. Shared by the runner header and the vibe panel.
export function useCountdown(target?: string | null): string {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!target) return
    const id = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(id)
  }, [target])

  if (!target) return ""
  const end = new Date(target).getTime()
  if (Number.isNaN(end)) return ""

  const diff = end - now
  if (diff <= 0) return "Expired"

  const totalMinutes = Math.floor(diff / 60_000)
  const days = Math.floor(totalMinutes / 1440)
  const hours = Math.floor((totalMinutes % 1440) / 60)
  const minutes = totalMinutes % 60

  if (days > 0) return `${days}d ${hours}h left`
  if (hours > 0) return `${hours}h ${minutes}m left`
  return `${minutes}m left`
}

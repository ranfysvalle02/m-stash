// A sandbox's claim token is returned only once, when the sandbox is created, so
// it is persisted per publishable key. The claim flow in the console reads it
// back to promote the sandbox into a live app. Shared by the try-it section (which
// writes it) and the console claim dialog (which reads and clears it).

export function claimStorageKey(publishableKey: string) {
  return `m-stash-claim:${publishableKey}`
}

export function saveClaimToken(publishableKey: string, token: string) {
  try {
    localStorage.setItem(claimStorageKey(publishableKey), token)
  } catch {
    // Best-effort; the token also lives in component state for this session.
  }
}

export function loadClaimToken(publishableKey: string): string | null {
  try {
    return localStorage.getItem(claimStorageKey(publishableKey))
  } catch {
    return null
  }
}

// keepPath is the console deep link that claims a sandbox. The app name rides
// along so the console can say what is being kept.
export function keepPath(publishableKey: string, appName?: string) {
  const params = new URLSearchParams({ claim: publishableKey })
  if (appName) params.set("name", appName)
  return `/console?${params.toString()}`
}

// keepAnywhereLink lets the sandbox be kept from another device. The claim
// token travels in the URL fragment, which browsers never send to a server, so
// it stays out of request logs and referrers; the console moves it into
// storage on arrival and scrubs it from the address bar.
export function keepAnywhereLink(publishableKey: string, token: string, appName?: string) {
  return `${window.location.origin}${keepPath(publishableKey, appName)}#t=${encodeURIComponent(token)}`
}

// adoptClaimFragment stores a #t=<token> from a keep link for the claimed key
// and removes it from the address bar. Call it before the claim dialog reads.
export function adoptClaimFragment(publishableKey: string | null) {
  const match = /^#t=([^&]+)$/.exec(window.location.hash)
  if (!match) return
  if (publishableKey) saveClaimToken(publishableKey, decodeURIComponent(match[1]))
  window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search)
}

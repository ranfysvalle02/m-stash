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

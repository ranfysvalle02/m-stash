// Pure helpers for the builder console's sign-in flow, kept out of App.tsx so
// they can be unit-tested.

export type ConsoleAuthMode = "signin" | "signup"

// consoleAuthMode picks the sign-in form's tab. The visitor's explicit choice
// wins; otherwise a keep link (claimPk) opens on "Create account". It is derived
// on every render because signupEnabled arrives after mount: freezing it at
// mount once sent every keep link to "Sign in".
export function consoleAuthMode(signupEnabled: boolean, picked: ConsoleAuthMode | null, claimPk: string | null): ConsoleAuthMode {
  if (!signupEnabled) return "signin"
  return picked ?? (claimPk ? "signup" : "signin")
}

// takeOAuthReturn reads a provider redirect's ?oauth_code / ?oauth_error once
// and scrubs both from the URL (keeping ?claim and ?name), so the one-time
// code can't be replayed from history or a bookmark.
export function takeOAuthReturn(): { code: string | null; error: string | null } {
  const params = new URLSearchParams(window.location.search)
  const code = params.get("oauth_code")
  const error = params.get("oauth_error")
  if (code || error) {
    params.delete("oauth_code")
    params.delete("oauth_error")
    const query = params.toString()
    window.history.replaceState(window.history.state, "", window.location.pathname + (query ? `?${query}` : ""))
  }
  return { code, error }
}

// onceByKey makes an async call idempotent per key. A one-time code must be
// redeemed exactly once even when React (StrictMode in development) runs the
// effect twice; both runs share the first call's promise.
export function onceByKey<T>(call: (key: string) => Promise<T>): (key: string) => Promise<T> {
  const pending = new Map<string, Promise<T>>()
  return (key) => {
    let promise = pending.get(key)
    if (!promise) {
      promise = call(key)
      pending.set(key, promise)
    }
    return promise
  }
}

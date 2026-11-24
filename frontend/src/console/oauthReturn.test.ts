// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest"
import { consoleAuthMode, onceByKey, takeOAuthReturn } from "./oauthReturn"

afterEach(() => window.history.replaceState(null, "", "/"))

describe("consoleAuthMode", () => {
  it("opens a keep link on Create account once signup is known to be open", () => {
    // First render: the manifest hasn't loaded, so signup reads as closed...
    expect(consoleAuthMode(false, null, "pk_abc")).toBe("signin")
    // ...and once it loads the default must follow, not stay frozen.
    expect(consoleAuthMode(true, null, "pk_abc")).toBe("signup")
  })

  it("respects the visitor's tab choice and defaults to sign in without a keep link", () => {
    expect(consoleAuthMode(true, "signin", "pk_abc")).toBe("signin")
    expect(consoleAuthMode(true, null, null)).toBe("signin")
    expect(consoleAuthMode(true, "signup", null)).toBe("signup")
  })

  it("never offers Create account when signup is closed", () => {
    expect(consoleAuthMode(false, "signup", "pk_abc")).toBe("signin")
  })
})

describe("takeOAuthReturn", () => {
  it("returns the code and scrubs it while keeping the keep-link params", () => {
    window.history.replaceState(null, "", "/console?claim=pk_abc&name=Shop&oauth_code=otc_123")
    expect(takeOAuthReturn()).toEqual({ code: "otc_123", error: null })
    expect(window.location.search).toBe("?claim=pk_abc&name=Shop")
  })

  it("returns provider errors and leaves a clean URL", () => {
    window.history.replaceState(null, "", "/console?oauth_error=signup_closed")
    expect(takeOAuthReturn()).toEqual({ code: null, error: "signup_closed" })
    expect(window.location.pathname + window.location.search).toBe("/console")
  })

  it("is a no-op without a provider return", () => {
    window.history.replaceState(null, "", "/console?claim=pk_abc")
    expect(takeOAuthReturn()).toEqual({ code: null, error: null })
    expect(window.location.search).toBe("?claim=pk_abc")
  })
})

describe("onceByKey", () => {
  it("redeems a one-time code once even when called twice (StrictMode double effect)", async () => {
    const exchange = vi.fn(async (code: string) => ({ code }))
    const once = onceByKey(exchange)
    const [first, second] = await Promise.all([once("otc_1"), once("otc_1")])
    expect(exchange).toHaveBeenCalledTimes(1)
    expect(first).toBe(second)
    await once("otc_2")
    expect(exchange).toHaveBeenCalledTimes(2)
  })
})

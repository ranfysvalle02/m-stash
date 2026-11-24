// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest"
import { adoptClaimFragment, keepAnywhereLink, keepPath, loadClaimToken, saveClaimToken } from "./claimStorage"

afterEach(() => {
  localStorage.clear()
  window.history.replaceState(null, "", "/")
})

describe("keep links", () => {
  it("encodes the key and app name into the console deep link", () => {
    expect(keepPath("pk_abc", "Riff & Groove")).toBe("/console?claim=pk_abc&name=Riff+%26+Groove")
    expect(keepPath("pk_abc")).toBe("/console?claim=pk_abc")
  })

  it("carries the claim token only in the fragment, which browsers never send to a server", () => {
    const link = new URL(keepAnywhereLink("pk_abc", "tok/en+1", "Shop"))
    expect(link.origin).toBe(window.location.origin)
    expect(link.pathname + link.search).toBe("/console?claim=pk_abc&name=Shop")
    expect(link.hash).toBe("#t=tok%2Fen%2B1")
    expect(link.search).not.toContain("tok")
  })
})

describe("adoptClaimFragment", () => {
  it("stores the token for the claimed key and scrubs it from the address bar", () => {
    window.history.replaceState(null, "", "/console?claim=pk_abc&name=Shop#t=tok%2Fen%2B1")
    adoptClaimFragment("pk_abc")
    expect(loadClaimToken("pk_abc")).toBe("tok/en+1")
    expect(window.location.hash).toBe("")
    expect(window.location.search).toBe("?claim=pk_abc&name=Shop")
  })

  it("round-trips a link built by keepAnywhereLink", () => {
    const link = new URL(keepAnywhereLink("pk_round", "a1b2c3", "Shop"))
    window.history.replaceState(null, "", link.pathname + link.search + link.hash)
    adoptClaimFragment("pk_round")
    expect(loadClaimToken("pk_round")).toBe("a1b2c3")
  })

  it("still scrubs the token when there is no key to store it under", () => {
    window.history.replaceState(null, "", "/console#t=secret")
    adoptClaimFragment(null)
    expect(window.location.hash).toBe("")
    expect(localStorage.length).toBe(0)
  })

  it("leaves unrelated fragments and existing tokens alone", () => {
    saveClaimToken("pk_abc", "keep-me")
    window.history.replaceState(null, "", "/console?claim=pk_abc#section")
    adoptClaimFragment("pk_abc")
    expect(window.location.hash).toBe("#section")
    expect(loadClaimToken("pk_abc")).toBe("keep-me")
  })
})

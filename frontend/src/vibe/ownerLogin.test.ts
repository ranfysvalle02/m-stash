import { describe, expect, it } from "vitest"
import { usableSandboxOwner } from "./ownerLogin"

describe("usableSandboxOwner", () => {
  const owner = { email: "owner-1@sandbox.invalid", password: "x" }
  it("offers the generated owner while the app is a sandbox", () => {
    expect(usableSandboxOwner(owner, false)).toBe(owner)
  })
  it("withholds it once the app is kept, because the console replaces it", () => {
    expect(usableSandboxOwner(owner, true)).toBeNull()
  })
})

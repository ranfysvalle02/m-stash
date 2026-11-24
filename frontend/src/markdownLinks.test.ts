import { describe, expect, it } from "vitest"
import { inAppPath } from "./markdownLinks"

const origin = "https://m-stash.example"

describe("inAppPath", () => {
  it("keeps app routes inside the SPA, including same-origin absolute links", () => {
    expect(inAppPath("/tutorials/login-and-private-data", origin)).toBe("/tutorials/login-and-private-data")
    expect(inAppPath(`${origin}/vibe`, origin)).toBe("/vibe")
    expect(inAppPath(`${origin}/console?claim=pk_1`, origin)).toBe("/console?claim=pk_1")
    expect(inAppPath(origin, origin)).toBe("/")
  })

  it("sends API routes, raw files, and other sites through real navigation", () => {
    for (const href of [
      "/v1/tutorials/x.md",
      `${origin}/mstash.js`,
      `${origin}/llms.txt`,
      "/skill.md?download=1",
      "https://github.com/ranfysvalle02/m-stash",
      "//evil.example/vibe",
      "http://localhost:8000",
      `${origin}.evil.example/vibe`,
      "#section",
      "mailto:hi@example.com",
    ]) {
      expect(inAppPath(href, origin), href).toBeNull()
    }
  })
})

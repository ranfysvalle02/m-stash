import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";

const client = readFileSync(new URL("../vibe_starter/mstash.js", import.meta.url), "utf8");
const wrapper = readFileSync(new URL("../vibe_starter/mstash-embed.js", import.meta.url), "utf8");
const exports = "\nexport { MStashError, createClient };\n";

function loadEmbed(dataset, fetch) {
  if (!client.endsWith(exports)) throw new Error("Client export marker is missing");
  const window = {};
  runInNewContext(`(function () {\n${client.slice(0, -exports.length)}\n${wrapper}\n})();`, {
    window,
    document: { currentScript: { src: "https://cdn.example/mstash-embed.js", dataset } },
    fetch,
    URL,
    URLSearchParams,
  });
  return window;
}

describe("standalone browser client", () => {
  it("authenticates and uses the existing resource API without cookies", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce({ ok: true, text: async () => JSON.stringify({ token: "user-token" }) })
      .mockResolvedValue({ ok: true, text: async () => JSON.stringify({ data: { slug: "main" } }) });
    const { mStash, MStashClient } = loadEmbed({ apiUrl: "https://api.example/", appKey: "pk_app" }, fetch);
    await mStash.signup({ email: "a@example.com", password: "long-password", username: "user" });
    expect(mStash.token).toBe("user-token");
    const record = { slug: "main", title: "Profile", visibility: "private", data: {} };
    await mStash.savePersonal("profile", record);
    await mStash.updatePersonal("profile", "main", record);
    expect((await mStash.getPersonal("profile", "main")).data.slug).toBe("main");
    await mStash.getShared("config", "main");
    await mStash.getPublicShared("config", "main");
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      "https://api.example/v1/auth/signup",
      "https://api.example/v1/me/resources/profile",
      "https://api.example/v1/me/resources/profile/main",
      "https://api.example/v1/me/resources/profile/main",
      "https://api.example/v1/shared/resources/config/main",
      "https://api.example/v1/public/shared/resources/config/main",
    ]);
    expect(fetch.mock.calls[1][1]).toMatchObject({
      method: "POST",
      credentials: "omit",
      headers: { "X-M-Stash-App": "pk_app", Authorization: "Bearer user-token" },
      body: JSON.stringify(record),
    });
    expect(fetch.mock.calls[2][1].method).toBe("PUT");
    const other = new MStashClient({ apiUrl: "https://other.example", appKey: "pk_other" });
    expect(other.token).toBeNull();
    mStash.logout();
    expect(mStash.token).toBeNull();
  });

  it("defaults to the script origin, rejects secret keys, and surfaces API errors", async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: false, status: 409, text: async () => JSON.stringify({ error: "already exists", code: "email_taken" }),
    });
    const { mStash, MStashClient } = loadEmbed({ appKey: "pk_app" }, fetch);
    await expect(mStash.login({ email: "a@example.com", password: "password" }))
      .rejects.toMatchObject({ message: "already exists", status: 409, body: { code: "email_taken" } });
    expect(fetch.mock.calls[0][0]).toBe("https://cdn.example/v1/auth/login");
    expect(() => new MStashClient({ appKey: "sk_secret" })).toThrow("publishable");
    expect(() => new MStashClient({ appKey: "pk_app", token: "sk_secret" })).toThrow("Secret");
    expect(() => loadEmbed({}, fetch)).toThrow("publishable");
  });
});

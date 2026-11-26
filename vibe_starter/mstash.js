// @ts-self-types="./mstash.d.ts"
// mstash.js — a zero-dependency m-stash client for browsers and Node 18+.
//
//   import { createClient } from "./mstash.js";
//   const stash = createClient({ api: "https://…", app: "pk_…" });
//   await stash.signup({ email, password, username }); // keeps the token
//   await stash.me.create("notes", { slug: "first", title: "First", visibility: "private", data: { body: "hi" } });
//   const { data } = await stash.me.list("notes");
//
// Server-side shared writes: createClient({ api, app, token: process.env.M_STASH_SECRET_KEY }).
// Never ship a secret key (sk_…) to a browser; the publishable key (pk_…) is safe there.
// TypeScript: keep mstash.d.ts next to this file and the types are picked up automatically.

class MStashError extends Error {
  constructor(status, body) {
    super((body && body.error) || `m-stash request failed with HTTP ${status}`);
    this.name = "MStashError";
    this.status = status;
    this.body = body;
  }
}

function createClient({ api, app, token = null, fetch: fetchImpl = globalThis.fetch } = {}) {
  if (!api || !app) throw new Error("createClient needs { api, app }");
  const base = String(api).replace(/\/+$/, "");
  let bearer = token;

  async function call(method, path, body, query) {
    const headers = { "X-M-Stash-App": app };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (bearer) headers.Authorization = `Bearer ${bearer}`;
    const search = query ? new URLSearchParams(Object.entries(query).filter(([, value]) => value != null)).toString() : "";
    const response = await fetchImpl(base + path + (search ? `?${search}` : ""), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
    });
    const text = await response.text();
    let payload = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      payload = { error: text };
    }
    if (!response.ok) throw new MStashError(response.status, payload);
    return payload;
  }

  const seg = encodeURIComponent;

  function resources(prefix) {
    return {
      // list returns { data: [...], page: { limit, nextCursor } }.
      list: (type, { limit, cursor } = {}) => call("GET", `${prefix}/${seg(type)}`, undefined, { limit, cursor }),
      get: (type, slug) => call("GET", `${prefix}/${seg(type)}/${seg(slug)}`),
      // record: { slug, title, visibility: "private" | "public" | "authenticated" (shared only), data, summary?, content? }
      create: (type, record) => call("POST", `${prefix}/${seg(type)}`, record),
      update: (type, slug, record) => call("PUT", `${prefix}/${seg(type)}/${seg(slug)}`, record),
      // increment is atomic: never read-modify-write a counter.
      increment: (type, slug, field, by = 1) =>
        call("PATCH", `${prefix}/${seg(type)}/${seg(slug)}`, {
          op: "increment",
          field: field.startsWith("data.") ? field : `data.${field}`,
          by,
        }),
      remove: (type, slug) => call("DELETE", `${prefix}/${seg(type)}/${seg(slug)}`),
    };
  }

  return {
    get token() {
      return bearer;
    },
    setToken(next) {
      bearer = next || null;
    },
    async signup({ email, password, username }) {
      const result = await call("POST", "/v1/auth/signup", { email, password, username });
      bearer = result.token;
      return result;
    },
    async login({ email, password }) {
      const result = await call("POST", "/v1/auth/login", { email, password });
      bearer = result.token;
      return result;
    },
    logout() {
      bearer = null;
    },
    // The signed-in user's own records (private unless saved as public).
    me: resources("/v1/me/resources"),
    // App-wide records: any signed-in user reads; the owner (admin) or a secret key writes.
    shared: resources("/v1/shared/resources"),
    // Public reads, no login.
    public: {
      list: (type, { limit, cursor } = {}) => call("GET", `/v1/public/shared/resources/${seg(type)}`, undefined, { limit, cursor }),
      get: (type, slug) => call("GET", `/v1/public/shared/resources/${seg(type)}/${seg(slug)}`),
      user: (username, type, slug) =>
        call("GET", `/v1/public/${seg(username)}/${seg(type)}${slug ? `/${seg(slug)}` : ""}`),
    },
  };
}

export { MStashError, createClient };

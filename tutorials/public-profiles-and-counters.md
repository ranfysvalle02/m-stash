---
title: Public profiles, shared catalogs, and counters
summary: Publish records anyone can read, give every member a public page, and count likes or streaks atomically.
minutes: 10
level: Intermediate
order: 3
---

Private data is the default. This tutorial covers the three ways data becomes public in m-stash, plus atomic counters. The snippets use the `mstash.js` client and run in a browser module or in Node 18+:

```js
import { createClient } from "{{API}}/mstash.js"; // in Node: download it first, see below
const api = "{{API}}";
const app = "pk_your_publishable_key";
```

In Node, grab the client once with `curl -O {{API}}/mstash.js` and import `./mstash.js`.

## Three kinds of public

| Who writes | Where it lives | Who reads |
| --- | --- | --- |
| The app owner | `shared` — app-wide records | Everyone, with `visibility: "public"` |
| Each member | `me` — their own space | Everyone, on the member's public page, with `visibility: "public"` |
| Each member | `me` | Only the member, with `visibility: "private"` |

## 1. A shared catalog the owner publishes

App-wide data is written by the owner (an admin login on the app) or by your server with the secret key. Members can read it but never write it.

```js
const owner = createClient({ api, app });
await owner.login({ email: "owner@example.com", password: "your-owner-password" });

await owner.shared.create("events", {
  slug: "launch-party",
  title: "Launch party",
  summary: "Friday at 7pm",
  visibility: "public",
  data: { date: "2026-12-05", city: "Brooklyn" },
});
```

Anyone can read it — no login, no token:

```js
const visitor = createClient({ api, app });
const { data: events } = await visitor.public.list("events"); // previews: slug, title, summary
const party = await visitor.public.get("events", "launch-party"); // the full record, with data
```

Lists return lightweight previews (`slug`, `title`, `summary`, timestamps); fetch a single record when you need its `data`.

## 2. A public page for every member

Every member gets a page at their username. Records they save with `visibility: "public"` appear on it; private ones never do.

```js
const ada = createClient({ api, app });
await ada.signup({ email: "ada@example.com", password: "a-long-password-here", username: "ada" });

await ada.me.create("projects", {
  slug: "analytical-engine",
  title: "Analytical Engine",
  visibility: "public",
  data: { stars: 0, url: "https://example.com/engine" },
});
await ada.me.create("projects", {
  slug: "secret-draft",
  title: "Secret draft",
  visibility: "private",
  data: {},
});

const { data: page } = await visitor.public.user("ada", "projects");
// -> only "analytical-engine"
```

The same page is plain HTTP, so it works from anywhere: `GET {{API}}/v1/public/ada/projects` with the `X-M-Stash-App` header.

## 3. Atomic counters

Never read a number, add one, and write it back — two clicks at once lose an update. Increment instead:

```js
await ada.me.increment("projects", "analytical-engine", "stars", 1);
const project = await ada.me.get("projects", "analytical-engine");
// project.data.data.stars === 1
```

`increment` works on any numeric `data` field and accepts negative steps. A member can increment their own records; app-wide counters (say, likes on a shared event) are incremented by the owner or by your server — see [Run a backend with your secret key](/tutorials/backend-with-your-secret-key).

## What you can't do (on purpose)

- A member can't write to `shared` — they get `403 Only administrators can write shared resources`.
- Nobody can read another member's private records, whatever the frontend sends.
- `authenticated` visibility (any signed-in member can read) exists only for `shared` records.

These aren't settings you might forget to turn on; they're the shape of the API.

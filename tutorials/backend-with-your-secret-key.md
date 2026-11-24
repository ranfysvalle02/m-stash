---
title: Run a backend with your secret key
summary: Publish app-wide data from a script or server, keep the secret out of the browser, and trust which user is calling you.
minutes: 10
level: Intermediate
order: 4
---

Your frontend uses the publishable key. Some jobs belong on a server: importing a catalog, publishing a daily leaderboard, or bumping a shared like counter. For those, m-stash gives every app one **secret key** (`sk_...`).

## 1. Get the secret key

The secret is shown once, when it is issued, and stored only as a hash — m-stash can't show it to you again. In the [console]({{API}}/console), click **Rotate secret** on your app and copy the new `sk_...` value. (Rotating replaces the old one, so update anything that used it.)

## 2. Set up a project

The build kit already contains `mstash.js` and `.env.example`. Starting from scratch instead:

```sh
mkdir stash-worker && cd stash-worker
curl -O {{API}}/mstash.js
cat > .env <<'ENV'
M_STASH_API={{API}}
M_STASH_APP_KEY=pk_your_publishable_key
M_STASH_SECRET_KEY=sk_your_secret_key
ENV
echo ".env" >> .gitignore
```

Never commit `.env`, and never ship the secret to a browser.

## 3. Publish shared data

Save as `publish.mjs`:

```js
import { createClient } from "./mstash.js";

const stash = createClient({
  api: process.env.M_STASH_API,
  app: process.env.M_STASH_APP_KEY,
  token: process.env.M_STASH_SECRET_KEY, // server only
});

const today = new Date().toISOString().slice(0, 10);
const record = {
  slug: `menu-${today}`,
  title: `Menu for ${today}`,
  visibility: "public",
  data: { date: today, dishes: ["Tonkotsu ramen", "Pork gyoza"], likes: 0 },
};

try {
  await stash.shared.create("menus", record);
} catch (error) {
  if (error.status !== 409) throw error;
  await stash.shared.update("menus", record.slug, record); // already there: replace it
}

await stash.shared.increment("menus", record.slug, "likes", 1);
const { data: published } = await stash.public.get("menus", record.slug); // read back through the public API
console.log(JSON.stringify(published, null, 2));
```

Run it (Node 20.6+ reads `.env` itself):

```sh
node --env-file=.env publish.mjs
```

Run it from cron or a scheduled job and your app's public menu updates itself. The secret key can only **change shared data** — create, update, increment, delete. It is write-only: it can't read through `shared` (an increment returns no body; use `public` for public records), and it can never touch a member's private records.

## 4. Trust the user calling your server

When your frontend calls your own server, forward the user's bearer token. Your server asks m-stash who it belongs to:

```js
async function currentUser(request) {
  const response = await fetch(`${process.env.M_STASH_API}/v1/auth/verify`, {
    headers: {
      "X-M-Stash-App": process.env.M_STASH_APP_KEY,
      Authorization: request.headers.get("authorization") ?? "",
    },
  });
  if (!response.ok) return null;
  const { claims } = await response.json();
  return claims; // { uid, email, role, ... }
}
```

Now a "like" endpoint on your server can check `currentUser`, then call `stash.shared.increment(...)` with the secret key — members get to like shared items without ever holding write access.

## Checklist

- The secret lives in `.env` or your host's secret store, never in frontend code or git.
- Shared writes happen on the server; members write only to their own `me` space.
- Rotate the secret if it ever leaks; the publishable key and all data stay the same.

# Build a new app on m-stash

Zero to auth + per-user data in ~5 minutes. No database wiring, no security rules.

> Already deployed m-stash? Skip to step 2. Need to deploy it first? See [quickstart.md](quickstart.md).

## 1. Point at a deployment

```sh
export API_URL='https://m-stash.onrender.com'   # your deployment
```

## 2. Create your app

One deployment hosts many apps. Mint one with the platform token — you get back a public key (safe in the browser) and a one-time secret (backend only).

```sh
curl -X POST "$API_URL/v1/apps" \
  -H "Authorization: Bearer $PLATFORM_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"slug":"my-app","displayName":"My App","adminEmail":"you@example.com","adminPassword":"a-long-unique-password"}'
```

```sh
export APP_KEY='pk_...'   # from the response — ships in your frontend
```

Every call below carries `X-M-Stash-App: $APP_KEY`.

## 3. Sign a user up

Your users sign up in *your* app. No m-stash account required.

```js
const res = await fetch(`${API_URL}/v1/auth/signup`, {
  method: "POST",
  headers: { "X-M-Stash-App": APP_KEY, "Content-Type": "application/json" },
  credentials: "include",
  body: JSON.stringify({
    email: "player@example.com",
    password: "a-long-unique-password",
    username: "player-one",
  }),
});
const { token } = await res.json();
```

Browser signup also sets an HttpOnly session cookie. Keep `token` for native/server clients.

## 4. Store data that belongs to them

The server derives the owner from the session — the browser never picks an owner or sees the database.

```js
await fetch(`${API_URL}/v1/me/resources/save`, {
  method: "POST",
  headers: { "X-M-Stash-App": APP_KEY, "Content-Type": "application/json" },
  credentials: "include",
  body: JSON.stringify({
    slug: "current",
    title: "Save slot",
    data: { level: 7, coins: 120 },
    visibility: "private",
  }),
});
```

## 5. Read it back

```js
const save = await fetch(`${API_URL}/v1/me/resources/save/current`, {
  headers: { "X-M-Stash-App": APP_KEY },
  credentials: "include",
}).then((r) => r.json());
```

That's the whole move: the server owns the data, your app just asks.

---

- `visibility`: `private` (owner only) · `authenticated` (any signed-in user) · `public` (anyone, served at `/{username}/{type}/{slug}`)
- Shared app config, flags, catalogs, leaderboards → [quickstart.md](quickstart.md#5-store-shared-application-data)
- Full API shapes & rules → [integration.md](integration.md)

Stash it. ’Stache it.

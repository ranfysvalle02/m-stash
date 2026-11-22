# m-stash

**Give your AI-built app sign-up, login, and private per-user data—without wiring a database or writing security rules.**

m-stash is a small, self-hostable, multi-tenant API between your app and MongoDB. One deployment hosts many isolated apps, plus ephemeral sandboxes a visitor can try and then keep. Your users sign up in your product with email and password, or with GitHub and Google when you configure those providers. m-stash derives record ownership from their authenticated identity and enforces access on every request. MongoDB stays off the client.

Use it for profiles, preferences, game saves, and app-wide configuration. Personal records belong to users; shared records belong to the app. New records are private by default, with deliberate options for signed-in or public access.

**Start building:** new here? The [5-minute build guide](quick.md) goes from a new app to per-user data. To run your own instance, follow the [Quickstart](quickstart.md) to deploy m-stash and provision an app. The hosted site is a demo, not a developer-account portal; app provisioning uses a platform token.

## Applications

One deployment hosts many isolated applications. Each app is created once through the platform control plane and receives two keys:

- A **publishable key** (`pk_…`) identifies the app. It is safe in frontend code and is sent on every request in the `X-M-Stash-App` header (or an `?app=` query parameter for anonymous browser calls).
- A **secret key** (`sk_…`) is a backend-only credential for shared-resource mutations. It is shown once at creation and stored only as a hash.

Data is isolated per app by construction: usernames, emails, namespaces, resources, and webhooks are all scoped to the owning app, so one app can never read or write another app's data. MongoDB stays invisible to the application developer.

```sh
curl --request POST "$API_URL/v1/apps" \
  --header "Authorization: Bearer $PLATFORM_TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"slug":"my-app","displayName":"My App","adminEmail":"admin@example.com","adminPassword":"use-a-long-unique-password"}'
```

The response returns the app record (including its `publishableKey`) and the one-time `secretKey`.

## The model

m-stash has two deliberately separate scopes:

| Plane | Provisioning | What it controls |
| --- | --- | --- |
| **Personal scope** | Open `POST /v1/auth/signup`, or OAuth when a provider is configured | A user claims one immutable username (unique within the app) and owns the records in their scope. |
| **Shared app scope** | Created per app during provisioning (`POST /v1/apps`) | The app's administrator manages app records for every user without claiming a public username. |

A signup creates a user, personal namespace, and owner membership in one MongoDB transaction, all scoped to the request's app. The caller never supplies an owner ID, namespace ID, or app ID. An administrator is an authentication principal with an `admin` role in its app; it may also be a normal registered user, but m-stash never invents a personal namespace for it.

## Data scopes

### Personal data

Each registered account gets a stable route such as `/alex`. Its typed resources live at `/alex/profile/main`, `/alex/save/slot-one`, or `/alex/portfolio/demo`.

- Only the owner can read or write the authenticated personal API.
- Personal resources are `private` or `public`.
- A public resource is visible only when the personal namespace is public.

### Shared app data

Every deployment has one managed shared scope for application-controlled data. It is the home for facts shared by the whole app: an app name and version, configuration, feature flags, catalogs, leaderboards, announcements, and server-computed results. Deployment administrators and trusted services own this data; users consume the visibility level the deployment publishes.

Types are reusable data buckets, such as `config`, `feature-flag`, `catalog`, or `leaderboard`. Visibility belongs to each record, not the bucket: an administrator can keep one `config` record private while publishing another. Public deployment records have human routes at `/public/{type}/{slug}` in the embedded UI and anonymous API routes at `/v1/public/shared/resources/{type}/{slug}`. Browse a public bucket at `/public/{type}` or `/v1/public/shared/resources/{type}`.

- Every signed-in user may read `authenticated` and `public` shared resources.
- Only an administrator or trusted backend service may write shared resources.
- `private` shared resources are visible only to administrators.
- `public` shared resources are available on named anonymous routes.

A per-app `secretKey` can be supplied to a trusted backend for shared resource mutations only. It cannot read resources or access a personal namespace.

## API surface

| Route | Purpose |
| --- | --- |
| `POST /v1/apps` | Platform-only: provision an app and return its publishable + secret keys. Requires `PLATFORM_TOKEN`. |
| `POST /v1/auth/signup` | Open registration: account + immutable personal namespace. |
| `POST /v1/auth/login` | Email/password login with bearer token and browser session. |
| `GET /v1/auth/providers` | Lists configured social-login providers (`github`, `google`). Empty when none are set. |
| `GET /v1/auth/oauth/{provider}/start` | Begins OAuth2 + PKCE. Full-page navigation, not `fetch`. Query: `app` (publishable key) and `redirect_uri`. |
| `POST /v1/auth/oauth/exchange` | Trades the one-time `oauth_code` from the callback for a bearer token and browser session. |
| `GET` / `POST` / `DELETE /v1/me/identities…` | Authenticated: list, link, and unlink social providers. Unlinking the last sign-in method is refused. |
| `GET` / `PUT /v1/me/namespace` | Read or present the caller's personal namespace. |
| `GET` / `POST /v1/me/resources/{type}` | List or create personal resources. List supports `?filter=`, `?q=`, and `?count=1`. |
| `GET` / `PUT` / `PATCH` / `DELETE /v1/me/resources/{type}/{slug}` | Work with a personal resource (`PATCH` atomically increments a numeric `data.*` field). |
| `GET /v1/shared/namespace` | Read shared scope metadata as an authenticated user. |
| `GET` / `POST /v1/shared/resources/{type}` | Read shared data; writes require admin or the app's secret key. List supports `?filter=`, `?q=`, and `?count=1`. |
| `GET` / `PUT` / `PATCH` / `DELETE /v1/shared/resources/{type}/{slug}` | Work with one shared resource (`PATCH` atomically increments a numeric `data.*` field). |
| `GET` / `POST /v1/shared/webhooks`, `DELETE /v1/shared/webhooks/{id}` | Admin-only: register, list, and remove HMAC-signed outbox event webhooks. |
| `GET /v1/public/{username}/{type}/{slug}` | Read one public personal resource. |
| `GET /v1/public/shared/resources/{type}/{slug}` | Read one public shared resource. |
| `POST /v1/try` | Start a blank live sandbox (when `TRY_SANDBOX=on`). Returns a publishable key, an expiry, and a one-time claim token. |
| `POST /v1/builder/auth/signup` | Self-serve builder account (when `BUILDER_SIGNUP=open`). Returns a session on the free plan. |
| `POST /v1/builder/apps/claim` | Keep a sandbox as a live app. Needs a builder session and the sandbox's claim token; the `pk_` and all data stay, and a new secret key is returned once. |

Social login is optional. When GitHub or Google credentials are set, `/login` and `/signup` offer those providers. Send the browser to `/v1/auth/oauth/{provider}/start`; the user comes back to `redirect_uri?oauth_code=otc_…`, and the app exchanges that code within 60 seconds. The code is single-use, the JWT never travels in a URL, and `redirect_uri` must be this deployment's origin or an allowed origin. Client secrets stay in environment variables. The security model is written up in [blog-auth.md](blog-auth.md).

See [integration.md](integration.md) for request examples and [quickstart.md](quickstart.md) to deploy the service.

## Vibe lab

`/vibe` is an unauthenticated playground for feeling out m-stash before you write any code. Launch one of the curated **recipes**, or describe an app — say, "a habit tracker with private streaks and a public profile" — and m-stash sketches a data model, hands back copy-paste snippets, and spins up a **live sandbox** that runs against the real API: sign in with a throwaway account, save records, and watch ownership get enforced on every request. The lab (and `GET /v1/vibe/recipes`) unlocks whenever `TRY_SANDBOX=on` **or** `GEMINI_API_KEY` is set, and returns `404` only when both are off. Recipes and deterministic blueprints need no model key; a `GEMINI_API_KEY` adds AI-authored specs on top.

Everything is URL-powered. Share the link, download a `.zip` starter, or open the plan behind the floating `</>` button. Shares (link, live demo, and starter) expire on their own — `72h` by default, capped at 5 days — and MongoDB reclaims them with a TTL index. The app idea is length-limited and sanitized, and the model is told to treat it as untrusted input, so the prompt-injection surface stays small. If the model is unavailable or past the daily cap, the lab answers from built-in blueprints instead of erroring out.

## Try it, run it, keep it

Every m-stash app is either a **sandbox** (no owner, expires, tight limits, carries a one-time claim token) or a **live** app (owned, no expiry, limits set by the owner's plan). A visitor can try the service against a real backend, then keep that exact app — the publishable key never changes, so a downloaded `.zip` or pasted cURL keeps working.

- **Try** — with `TRY_SANDBOX=on`, `POST /v1/try` provisions a blank, expiring sandbox. The `/guide` page and every `/console` app card run the full Register → Log in → Create → View-yours → View-public loop live in the browser.
- **Keep** — with `BUILDER_SIGNUP=open`, a visitor creates a free builder account and claims the sandbox with its claim token. Claiming flips the app to live in place and keeps all users and records; a fresh secret key is shown once.
- **Limits** — free-plan builders are capped at `FREE_TIER_MAX_APPS` apps, and each app is bounded by `FREE_TIER_MAX_USERS`, `FREE_TIER_MAX_RECORDS` (per namespace), and `FREE_TIER_MAX_RECORD_BYTES` (a zero value means unlimited). Operator-provisioned builders (via `PLATFORM_TOKEN`) remain unbounded.

> **Note:** self-serve signup does **not** verify email addresses — there is no email transport. The `BUILDER_SIGNUP` flag, the per-IP and daily signup caps, and the free-tier quotas are the controls. Leave `BUILDER_SIGNUP=off` if you need verified accounts.

## Configuration

| Variable | Required | Purpose |
| --- | --- | --- |
| `MONGO_URI` | Yes | Replica-set or sharded MongoDB URI. |
| `MONGO_DB` | Yes | Database name. |
| `JWT_SECRET` | Yes | At least 32 characters; signs access and session tokens. |
| `PUBLIC_BASE_URL` | No | Canonical public origin, such as `https://m-stash.example`. Used for OAuth callbacks and shared links. Register `{PUBLIC_BASE_URL}/v1/auth/oauth/github/callback` and `…/google/callback` with each provider. |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | No | Enables GitHub sign-in. Set both or neither; a half-set pair fails boot. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | No | Enables Google sign-in. Set both or neither; a half-set pair fails boot. |
| `PLATFORM_TOKEN` | Yes | At least 32 characters; authorizes `POST /v1/apps` to provision applications. |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | Per app | Supplied in the `POST /v1/apps` body (not as environment variables) to create that app's administrator. |
| `ALLOWED_ORIGINS` | No | Comma-separated default allowed browser origins. Each app may also carry its own `allowedOrigins`. Blank or unset logs a startup warning and currently permits all origins for early development. |
| `METRICS_TOKEN` | No | Enables protected Prometheus metrics when at least 32 characters. |
| `SESSION_COOKIE_SECURE` | No | Set `true` behind HTTPS. |
| `DEMO_DATA` | No | Seeds a storytelling public demo into `/public` on boot. Default `on`; set `off` for a clean deployment. |
| `GEMINI_API_KEY` | No | Adds AI-authored specs to the `/vibe` lab (AI sketches an m-stash backend from an app idea). The lab's recipes and blueprints work without it; the lab is closed only when this is blank **and** `TRY_SANDBOX=off`. |
| `GEMINI_MODEL` | No | Model for the vibe lab. Defaults to `gemini-3.5-flash-lite`. |
| `VIBE_DAILY_LIMIT` | No | Global daily cap on model generations (plus a per-IP limit). Past the cap, or whenever the model fails, the lab answers from built-in blueprints. Defaults to `500`. |
| `VIBE_SHARE_TTL` | No | How long a vibe's share link, live demo, and `.zip` starter stay available before MongoDB expires them. Defaults to `72h`; capped at `120h` (5 days). |
| `VIBE_MAX_SANDBOXES` | No | Global cap on live sandbox tenants (vibe + try-it). Past it the lab still returns a spec and snippets, just without a live demo. Defaults to `1000`. |
| `TRY_SANDBOX` | No | Set `on` to enable the blank `POST /v1/try` sandbox used by the guide and console, and to unlock the Vibe lab's instant recipes and blueprints (no `GEMINI_API_KEY` needed). Defaults to `off` (endpoint returns 404). |
| `TRY_SANDBOX_TTL` | No | How long a try-it sandbox lives before the sweeper deletes it and its data. Defaults to `24h`; capped at `120h`. |
| `BUILDER_SIGNUP` | No | Set `open` to allow self-serve free-plan builder signup and sandbox claiming. Defaults to `off` (operator-provisioned accounts only). Emails are not verified. |
| `BUILDER_SIGNUP_DAILY_LIMIT` | No | Global daily cap on self-serve signups (per-IP is additionally limited to 5/hour). Defaults to `100`. |
| `FREE_TIER_MAX_APPS` | No | Apps a free-plan builder may own. Defaults to `3`. A zero value means unlimited. |
| `FREE_TIER_MAX_USERS` | No | User accounts per free-plan app. Defaults to `1000`. A zero value means unlimited. |
| `FREE_TIER_MAX_RECORDS` | No | Records per namespace in a free-plan app. Defaults to `2000`. A zero value means unlimited. |
| `FREE_TIER_MAX_RECORD_BYTES` | No | Max stored bytes per record in a free-plan app. Defaults to `65536`. A zero value means unlimited. |
| `TRUST_PROXY` | No | Set `true` only behind a trusted TLS-terminating proxy. |

The wildcard CORS default is intentionally insecure. Set explicit origins before a production browser client uses cross-origin authentication.

## Local development

MongoDB must be a replica set or sharded cluster because signups and mutations use transactions. The included Compose stack starts a private single-node replica set. Copy the environment template, set a 32-character-or-longer `JWT_SECRET`, then start the stack:

```sh
cp .env.example .env   # then edit JWT_SECRET and PLATFORM_TOKEN
make up                # or: docker compose up --build
```

Open `http://localhost:4000`. For frontend development, run the API on port `4000` and then:

```sh
npm --prefix frontend run dev
```

The production UI is embedded in the Go binary, so the Go build consumes `frontend/dist`. Use `make build`, `make test`, and `make check` rather than raw `go` commands, because they run `make ui-build` first; a bare `go test ./...` fails until the UI is built. Run `make help` to list every target. `/healthz`, `/readyz`, and `/.well-known/m-stash.json` support operations and discovery.

# m-stash

**Give your AI-built app sign-up, login, and private per-user data—without wiring a database or writing security rules.**

m-stash is a small, self-hostable API between your app and MongoDB. Your users sign up in your product; m-stash derives record ownership from their authenticated identity and enforces access on every request. MongoDB stays off the client.

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
| **Personal scope** | Open `POST /v1/auth/signup` | A user claims one immutable username (unique within the app) and owns the records in their scope. |
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
| `GET` / `PUT /v1/me/namespace` | Read or present the caller's personal namespace. |
| `GET` / `POST /v1/me/resources/{type}` | List or create personal resources. List supports `?filter=`, `?q=`, and `?count=1`. |
| `GET` / `PUT` / `PATCH` / `DELETE /v1/me/resources/{type}/{slug}` | Work with a personal resource (`PATCH` atomically increments a numeric `data.*` field). |
| `GET /v1/shared/namespace` | Read shared scope metadata as an authenticated user. |
| `GET` / `POST /v1/shared/resources/{type}` | Read shared data; writes require admin or the app's secret key. List supports `?filter=`, `?q=`, and `?count=1`. |
| `GET` / `PUT` / `PATCH` / `DELETE /v1/shared/resources/{type}/{slug}` | Work with one shared resource (`PATCH` atomically increments a numeric `data.*` field). |
| `GET` / `POST /v1/shared/webhooks`, `DELETE /v1/shared/webhooks/{id}` | Admin-only: register, list, and remove HMAC-signed outbox event webhooks. |
| `GET /v1/public/{username}/{type}/{slug}` | Read one public personal resource. |
| `GET /v1/public/shared/resources/{type}/{slug}` | Read one public shared resource. |

See [integration.md](integration.md) for request examples and [quickstart.md](quickstart.md) to deploy the service.

## Configuration

| Variable | Required | Purpose |
| --- | --- | --- |
| `MONGO_URI` | Yes | Replica-set or sharded MongoDB URI. |
| `MONGO_DB` | Yes | Database name. |
| `JWT_SECRET` | Yes | At least 32 characters; signs access and session tokens. |
| `PLATFORM_TOKEN` | Yes | At least 32 characters; authorizes `POST /v1/apps` to provision applications. |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | Per app | Supplied in the `POST /v1/apps` body (not as environment variables) to create that app's administrator. |
| `ALLOWED_ORIGINS` | No | Comma-separated default allowed browser origins. Each app may also carry its own `allowedOrigins`. Blank or unset logs a startup warning and currently permits all origins for early development. |
| `METRICS_TOKEN` | No | Enables protected Prometheus metrics when at least 32 characters. |
| `SESSION_COOKIE_SECURE` | No | Set `true` behind HTTPS. |
| `DEMO_DATA` | No | Seeds a storytelling public demo into `/public` on boot. Default `on`; set `off` for a clean deployment. |
| `GEMINI_API_KEY` | No | Enables the unauth `/vibe` lab (AI sketches an m-stash backend from an app idea). Blank disables the feature. |
| `GEMINI_MODEL` | No | Model for the vibe lab. Defaults to `gemini-flash-latest`. |
| `VIBE_DAILY_LIMIT` | No | Global daily cap on vibe-lab generations (plus a per-IP limit). Defaults to `500`. |
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

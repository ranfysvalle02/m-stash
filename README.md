# m-stash

m-stash is an ownership layer for application data. It sits between authentication and MongoDB, turning a signed-in identity into the server-enforced answer to: who owns this record, and who may read it?

Every application gets two scopes: personal records owned by an account and shared app records owned by the deployment. Typed buckets organize records; per-record visibility keeps them private, available to signed-in users, or deliberately public. Use it for profiles and preferences, game saves, feature configuration, catalogs, leaderboards, public portfolios, and community submissions without exposing MongoDB to a browser.

## The model

m-stash has two deliberately separate scopes:

| Plane | Provisioning | What it controls |
| --- | --- | --- |
| **Personal scope** | Open `POST /v1/auth/signup` | A user claims one immutable username and owns the records in their scope. |
| **Shared app scope** | `ADMIN_EMAIL` + `ADMIN_PASSWORD` environment variables | The deployment owner manages app records for every user without claiming a public username. |

A signup creates a user, personal namespace, and owner membership in one MongoDB transaction. The caller never supplies an owner ID or namespace ID. A deployment admin is an authentication principal with an `admin` role; it may also be a normal registered user, but m-stash never invents a personal namespace for it.

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

A `SERVICE_TOKEN` can be supplied to a trusted backend for shared resource mutations only. It cannot read resources or access a personal namespace.

## API surface

| Route | Purpose |
| --- | --- |
| `POST /v1/auth/signup` | Open registration: account + immutable personal namespace. |
| `POST /v1/auth/login` | Email/password login with bearer token and browser session. |
| `GET` / `PUT /v1/me/namespace` | Read or present the caller's personal namespace. |
| `GET` / `POST /v1/me/resources/{type}` | List or create personal resources. |
| `GET` / `PUT` / `DELETE /v1/me/resources/{type}/{slug}` | Work with a personal resource. |
| `GET /v1/shared/namespace` | Read shared scope metadata as an authenticated user. |
| `GET` / `POST /v1/shared/resources/{type}` | Read shared data; writes require admin or `SERVICE_TOKEN`. |
| `GET` / `PUT` / `DELETE /v1/shared/resources/{type}/{slug}` | Work with one shared resource. |
| `GET /v1/public/{username}/{type}/{slug}` | Read one public personal resource. |
| `GET /v1/public/shared/resources/{type}/{slug}` | Read one public shared resource. |

See [integration.md](integration.md) for request examples and [quickstart.md](quickstart.md) to deploy the service.

## Configuration

| Variable | Required | Purpose |
| --- | --- | --- |
| `MONGO_URI` | Yes | Replica-set or sharded MongoDB URI. |
| `MONGO_DB` | Yes | Database name. |
| `JWT_SECRET` | Yes | At least 32 characters; signs access and session tokens. |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | Optional pair | Creates or promotes a deployment administrator and initializes shared data. No username is required. |
| `SERVICE_TOKEN` | No | At least 32 characters; a backend-only credential for shared mutations. |
| `ALLOWED_ORIGINS` | No | Comma-separated allowed browser origins. Blank or unset currently permits all origins for early development. |
| `METRICS_TOKEN` | No | Enables protected Prometheus metrics when at least 32 characters. |
| `SESSION_COOKIE_SECURE` | No | Set `true` behind HTTPS. |
| `TRUST_PROXY` | No | Set `true` only behind a trusted TLS-terminating proxy. |

The wildcard CORS default is intentionally insecure. Set explicit origins before a production browser client uses cross-origin authentication.

## Local development

MongoDB must be a replica set or sharded cluster because signups and mutations use transactions. The included Compose stack starts a private single-node replica set:

```sh
JWT_SECRET='replace-with-a-32-character-minimum-secret' docker compose up --build
```

Open `http://localhost:4000`. For frontend development, run the API on port `4000` and then:

```sh
npm --prefix frontend run dev
```

The production UI is embedded in the Go binary. `/healthz`, `/readyz`, and `/.well-known/m-stash.json` support operations and discovery.

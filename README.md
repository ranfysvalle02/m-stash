# m-stash

`m-stash` is the secure access layer for MongoDB applications. Your application gets an authenticated API; m-stash checks each request against collection policy before MongoDB sees it. Browsers never receive database credentials.

It is intentionally narrow:

| Need | m-stash provides |
| --- | --- |
| **Secure by default** | Signup, login, rotating browser sessions, JWT verification, CSRF protection, and no MongoDB credentials in clients. |
| **Scoped access** | Per-collection document rules plus field allowlists keep each caller inside the data it is allowed to read or change. |
| **Built to operate** | Replica-set transactions, a durable mutation outbox, cursor pagination, rate limiting, health checks, and metrics support production workloads. |

Use it for profiles, inventories, application state, leaderboards, operational data, or any MongoDB collection that should have a controlled API boundary.

## Embedded workspace

The gateway serves an embedded, same-origin operator workspace at `/`. It lets you verify identity, policy boundaries, the included sample collection, and explicitly shared public items. It is compiled into the same Go binary and Docker image as the API, so no second frontend deployment is required.

For local UI development, start the API on port `4000` and run `npm --prefix frontend run dev`; Vite proxies `/v1` requests to the API. Run `make build` to compile the production UI and Go binary together, or `make test` to build the UI before running Go tests.

## Default surfaces

The default configuration separates identity, private data, and public data:

| Surface | Access | Purpose |
| --- | --- | --- |
| `_users` | Gateway only | Email, password hash, role, and account metadata. Never available through the database proxy. |
| `profiles` | Owner through authenticated API | A user's editable profile document. Its `_id` is the authenticated user's ID. |
| `GET /v1/public/profiles/{handle}` | Anyone | A stable public profile page. Only `handle`, `displayName`, `bio`, `avatarURL`, and `links` are returned. |
| `stashes` | Owner-managed; signed-in readers can also see public items | Included sample collection for verifying the policy-backed data path. Each item is private by default and can be shared with `isPublic: true`. |
| `GET /v1/public/profiles/{handle}/stashes` | Anyone | Cursor-paginated public items for one public profile, returned with a safe projection. |
| `GET /v1/public/stashes` | Anyone | Global cursor-paginated feed of compact public-item previews. |
| `GET /v1/public/stashes/{id}` | Anyone | Full public content for one explicitly shared item. |

Public profile reads bypass the general database proxy intentionally. The endpoint requires a lowercase handle and queries only `isPublic: true` documents with an allowlisted MongoDB projection. Adding private fields to `profiles` later cannot expose them by accident.

The same pattern applies to the included `stashes` collection. Owners create, edit, and delete only their own records through the authenticated API. Setting `isPublic: true` makes an item eligible for the global public feed; the profile-scoped feed additionally requires the owner's profile to be public. Visitors cannot call authenticated write routes.

Public item feeds are cursor-paginated and sorted by `createdAt` then `_id`, both descending. `limit` defaults to `20` and accepts values from `1` through `100`; `tag` optionally filters to one exact tag. Responses include an opaque `page.nextCursor`, which clients pass back as `cursor` to fetch the next page. The gateway creates compound indexes for both tagged and unfiltered profile feeds at startup.

```text
GET /v1/public/profiles/ada-lovelace/stashes?limit=20&tag=release
GET /v1/public/profiles/ada-lovelace/stashes?limit=20&cursor=<page.nextCursor>
```

```json
{
  "data": [{ "title": "Shipping notes", "tags": ["release"] }],
  "page": { "limit": 20, "nextCursor": "eyJjcmVhdGVkQXQiOiIuLi4ifQ" }
}
```

## Optional public discovery

`GET /v1/public/stashes` is a separate, intentionally small public-item API. It accepts the same `limit`, `cursor`, and exact `tag` filter as profile feeds, but it never accepts arbitrary MongoDB queries. It returns compact previews (`_id`, `title`, `summary`, `tags`, and timestamps), keeping feed payloads bounded; request the detail route when a visitor opens an item.

```text
GET /v1/public/stashes?limit=20&tag=release
GET /v1/public/stashes?limit=20&cursor=<page.nextCursor>
GET /v1/public/stashes/507f1f77bcf86cd799439011
```

The detail route returns the same allowlisted fields plus `content`. Global and profile feeds are backed by dedicated compound indexes, so clients can page by recency without a growing `skip` cost.

## Profile fields

Clients may write only these fields to `profiles`:

```json
{
  "handle": "ada-lovelace",
  "displayName": "Ada Lovelace",
  "bio": "Mathematician and programmer.",
  "avatarURL": "https://cdn.example.com/ada.jpg",
  "links": [{ "label": "Website", "url": "https://example.com" }],
  "isPublic": true
}
```

`_id` is hydrated from the JWT on insert and cannot be changed. `email`, `role`, and `permissions` are never writable through this API. A public handle contains 3-32 lowercase letters, numbers, hyphens, or underscores.

## Sample collection fields

The default `stashes` collection is a simple sample surface with a required `title` plus optional `summary`, `content`, and `tags`. Set `isPublic` to `true` to include an item in public listings. The gateway assigns `ownerId`, `createdAt`, and `updatedAt`; a supplied `ownerId` must exactly match the signed-in user, and timestamps cannot be supplied by clients. Add rules for the collections that represent your actual application model.

```json
{
  "title": "Shipping notes",
  "summary": "What changed in version one.",
  "content": "Long-form content lives here.",
  "tags": ["release", "product"],
  "isPublic": true
}
```

## Deploy

`m-stash` is distributed as a statically compiled Go binary in a small Alpine Docker image. It listens on `PORT` (default `4000`). Railway, Fly.io, and Render terminate TLS, exposing HTTP routes as HTTPS and WebSockets as WSS.

| Variable | Required | Purpose |
| --- | --- | --- |
| `MONGO_URI` | Hosted deployments | MongoDB or Atlas connection URI. Defaults to `mongodb://localhost:27017` for local development. |
| `MONGO_DB` | No | Database name. Defaults to `app_db`. |
| `JWT_SECRET` | Yes | At least 32 characters; used to sign user sessions. |
| `JWT_ISSUER` | No | JWT issuer. Defaults to `m-stash`. |
| `JWT_AUDIENCE` | No | JWT audience. Defaults to `m-stash`. |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | No | Create the initial admin account or promote the matching account on startup. Set both together; the password is used only when creating the account. |
| `ACCESS_TOKEN_TTL` | No | Lifetime for the UI's `HttpOnly` access cookie. Defaults to `15m`. |
| `REFRESH_SESSION_TTL` | No | Lifetime for rotating browser refresh sessions. Defaults to `720h`. |
| `SESSION_COOKIE_SECURE` | No | Require HTTPS for browser session cookies. Defaults to `true`; use `false` only for local HTTP development. |
| `TRUST_PROXY` | No | Trust forwarded HTTPS and client-IP information only when set to `true` behind a proxy you control. Defaults to `false`. |
| `METRICS_TOKEN` | Optional | At least 32 characters. Enables the protected Prometheus metrics endpoint; metrics stay disabled when omitted. |
| `ALLOWED_ORIGINS` | Recommended in production | Comma-separated browser origins, such as `https://app.example.com`. Defaults to `*`. |
| `PORT` | No | Listener port. Cloud providers set this automatically. |

For Atlas, configure network access for the deployment provider. Use a provider-supported static outbound address when available; do not assume there is one universal egress range for every plan.

### Docker Hub

Published releases are available for `linux/amd64` and `linux/arm64`:

```sh
docker pull YOUR_DOCKERHUB_USERNAME/m-stash:latest
docker run --rm -p 4000:4000 \
  -e MONGO_URI='mongodb://user:password@mongo:27017/?authSource=admin' \
  -e MONGO_DB='app_db' \
  -e JWT_SECRET='replace-with-a-long-random-secret' \
  -e ADMIN_EMAIL='admin@example.com' \
  -e ADMIN_PASSWORD='replace-with-a-long-admin-password' \
  -e ALLOWED_ORIGINS='https://app.example.com' \
  YOUR_DOCKERHUB_USERNAME/m-stash:latest
```

Pin production deployments to a release tag such as `:1.0.0`; `:latest` follows the newest stable release. The image runs as an unprivileged user, exposes port `4000`, and reports liveness at `/healthz` plus Mongo-backed readiness at `/readyz`.

### Local container

```sh
docker build -t m-stash .
docker run --rm -p 4000:4000 \
  -e MONGO_URI='mongodb+srv://...' \
  -e MONGO_DB='app_db' \
  -e JWT_SECRET='replace-with-a-long-random-secret' \
  -e ADMIN_EMAIL='admin@example.com' \
  -e ADMIN_PASSWORD='replace-with-a-long-admin-password' \
  -e ALLOWED_ORIGINS='http://localhost:3000' \
  m-stash
```

Check readiness with `curl http://localhost:4000/healthz`, then open `http://localhost:4000` to use the embedded workspace. Set `SESSION_COOKIE_SECURE=false` only for this local HTTP mode.

### Docker Compose

The included [compose.yaml](compose.yaml) starts an unauthenticated single-node MongoDB replica set on its private Docker network and the gateway together. This is intentionally for local development only; production must use a secured MongoDB deployment. Copy the example environment file, replace its secret placeholders, then start the stack:

```sh
cp .env.example .env
docker compose up --build --wait
curl http://localhost:4000/readyz
```

To use a published image instead of building locally, replace `build: .` and `image: m-stash:local` in [compose.yaml](compose.yaml) with `image: YOUR_DOCKERHUB_USERNAME/m-stash:1.0.0`.

For custom collection rules, mount a policy file at `/etc/m-stash/gateway.json`. The image sets `M_STASH_CONFIG` to that location. Keep `JWT_SECRET` and database credentials in environment variables or your secret manager rather than the policy file.

### Kubernetes

[k8s/m-stash.yaml](k8s/m-stash.yaml) contains a two-replica Deployment, ClusterIP Service, health probes, resource settings, a read-only ConfigMap-mounted policy, and restricted pod security settings. Its `MONGO_URI` must target a MongoDB replica set or sharded cluster because m-stash writes mutations and outbox events atomically. Before applying it:

1. Replace `YOUR_DOCKERHUB_USERNAME/m-stash:latest` with a pinned published tag.
2. Set the allowed origin and collection rules in the ConfigMap.
3. Create `m-stash-secrets` from your secret manager or use [k8s/m-stash-secrets.example.yaml](k8s/m-stash-secrets.example.yaml) as a local template. Do not commit real values.

```sh
kubectl apply -f k8s/m-stash.yaml
```

`/readyz` checks MongoDB and is used for readiness; `/healthz` is the liveness endpoint. Signup and login use a MongoDB-backed, fixed-window limiter shared by all replicas: 10 attempts per five minutes for each endpoint/client-IP pair. Identifiers are SHA-256 hashes and expire through a TTL index. Keep an ingress or API gateway in front for volumetric DDoS protection and broader edge controls.

### Publish a release

The [publish-image workflow](.github/workflows/publish-image.yml) publishes multi-architecture Docker Hub images when a `v*` Git tag is pushed. Create a GitHub Environment named `m-stash`, then add environment secrets named `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` (a Docker Hub access token with read/write permission). The job targets this environment and can use its protection rules before publishing. Create a release tag once the secrets are configured:

```sh
git tag v1.0.0
git push origin v1.0.0
```

This publishes `:1.0.0`, `:1.0`, `:1`, and `:latest` for stable semantic-version releases. Prereleases such as `v1.0.0-rc.1` do not update `:latest`.

### Railway

The included [railway.json](railway.json) configures Dockerfile builds and `/healthz` health checks.

```sh
railway init
railway variables set MONGO_URI='mongodb+srv://...' MONGO_DB='app_db' JWT_SECRET='replace-with-a-long-random-secret' ALLOWED_ORIGINS='https://app.example.com'
railway up
```

### Fly.io

```sh
fly launch --no-deploy
fly secrets set MONGO_URI='mongodb+srv://...' MONGO_DB='app_db' JWT_SECRET='replace-with-a-long-random-secret' ALLOWED_ORIGINS='https://app.example.com'
fly deploy
```

Use internal port `4000` when prompted.

### Render

The included [render.yaml](render.yaml) is a Render Blueprint. Push the repository to a Git provider, select **New** then **Blueprint**, and connect the repository. Before creating the service, set `MONGO_URI`, `ALLOWED_ORIGINS`, `ADMIN_EMAIL`, and `ADMIN_PASSWORD`; Render generates `JWT_SECRET` and `METRICS_TOKEN`, builds the Docker image, and verifies `/healthz`. The Blueprint enables trusted-proxy handling and secure browser cookies for Render's HTTPS edge.

For an existing Render service, add those variables under **Environment** and redeploy manually. Blueprint changes do not automatically backfill environment variables into an already-created service. Set `ALLOWED_ORIGINS` to the exact public origin, for example `https://m-stash.onrender.com`. Use the configured `ADMIN_EMAIL` and `ADMIN_PASSWORD` to log in after the redeploy; both values must be present together.

### Vercel

Vercel Functions cannot host persistent WebSocket connections or this long-lived server. Deploy the gateway to Railway, Fly.io, or Render, then use its public URL from a Vercel frontend.

## API

All database operations use `POST /v1/db/{collection}/{action}` and a JWT bearer token. Supported actions are `find`, `findOne`, `insertOne`, `updateOne`, and `deleteOne`. Generic client filters permit only `$and`, `$or`, `$eq`, `$ne`, `$in`, `$nin`, `$gt`, `$gte`, `$lt`, `$lte`, `$exists`, and `$all`; executable or expression operators such as `$where` and `$expr` are rejected.

```json
{
  "query": { "status": "active" },
  "limit": 50,
  "payload": { "title": "Example" }
}
```

`find` defaults to 50 documents and is capped at 100. Use purpose-built public feeds for cursor pagination rather than treating the proxy as a collection export API.

## Integration Contracts

Services can validate an incoming user token without sharing `JWT_SECRET` by forwarding it as a bearer token to `GET /v1/auth/verify`. A successful response contains `active: true` plus the standard `sub`, `iss`, `aud`, `jti`, issue/expiry metadata, and m-stash `uid`, `email`, and `role` claims. Tokens are signed with HS256 and require the configured issuer and audience.

`GET /.well-known/m-stash.json` exposes a versioned service manifest with the verification endpoint, available API surfaces, transactional-outbox schema version, and metrics availability. Every HTTP response includes `X-Request-ID` for cross-service correlation.

Extensions and workflows should run as separate services. The durable boundary for future workflow delivery is a transactional outbox and signed webhooks, not in-process plugins or best-effort goroutines; that keeps tenant code out of the gateway and lets workers scale, retry, and evolve independently.

## Transactional Outbox

Every successful authenticated `insertOne`, `updateOne`, or `deleteOne` creates an immutable event in `_m_stash_outbox` within the same MongoDB transaction as the document change. If the event cannot be recorded, the write is aborted. The service verifies transaction support at startup, so it requires a replica set or sharded MongoDB deployment.

Events use versioned names such as `m-stash.stashes.updateOne.v1` and contain an event `_id`, `occurredAt`, `requestId`, actor ID/role, collection/resource ID, changed field names or result counts, and delivery metadata. They begin as `pending`, with no payload values copied into the event. A separately deployed worker should claim events atomically, use the event `_id` as its idempotency key, lease/retry delivery, and move exhausted events to a dead-letter state. The generic gateway API permanently reserves `_m_stash_*` collections, so worker credentials should be scoped directly in MongoDB rather than exposed through this service.

The gateway intentionally does not dispatch webhooks itself. A worker can add signed delivery, tenant-aware destinations, retries, dead-letter handling, and SSRF controls without coupling third-party code or network access to request handling.

## Observability

Every request emits one JSON log entry with a sanitized request ID, normalized route, status, and duration. m-stash accepts a valid upstream `X-Request-ID` or generates one, then returns it in the response and persists it in outbox events.

Set `METRICS_TOKEN` to enable `GET /metrics`; call it with `Authorization: Bearer <METRICS_TOKEN>`. The endpoint exposes Prometheus-compatible HTTP request, in-flight request, process-start, committed outbox-event, and shared rate-limit rejection metrics. It returns `404` when metrics are not configured, preventing accidental exposure through a public ingress.

Persistent clients can use `wss://your-gateway.example/v1/ws/{collection}` with the same bearer token during the upgrade. Send the same JSON plus an `action` field.

Public clients use `GET /v1/public/profiles/{handle}` and `GET /v1/public/profiles/{handle}/stashes` without a token. The complete secure-MongoDB setup walkthrough is in [quickstart.md](quickstart.md); apps connecting to the deployed gateway can follow [integration.md](integration.md).

# m-stash

`m-stash` is a small Go gateway for MongoDB applications. It owns signup and login, verifies JWTs, applies document-level MongoDB JSON policies, and exposes a deliberate public-profile surface without exposing database credentials to browsers.

## Core model

The default configuration separates identity, private data, and public data:

| Surface | Access | Purpose |
| --- | --- | --- |
| `_users` | Gateway only | Email, password hash, role, and account metadata. Never available through the database proxy. |
| `profiles` | Owner through authenticated API | A user's editable profile document. Its `_id` is the authenticated user's ID. |
| `GET /v1/public/profiles/{handle}` | Anyone | A stable public profile page. Only `handle`, `displayName`, `bio`, `avatarURL`, and `links` are returned. |
| `stashes` | Owner-managed; signed-in readers can also see public stashes | A user's posts, notes, or saved work. Each stash is private by default or publishable with `isPublic: true`. |
| `GET /v1/public/profiles/{handle}/stashes` | Anyone | All public stashes for one published profile, returned with a safe public projection. |

Public profile reads bypass the general database proxy intentionally. The endpoint requires a lowercase handle and queries only `isPublic: true` documents with an allowlisted MongoDB projection. Adding private fields to `profiles` later cannot expose them by accident.

The same pattern applies to stashes. Owners create, edit, and delete only their own stashes through the authenticated API. Signed-in callers can also discover any published stash through the generic API; visitors use the profile-scoped public endpoint and cannot create, edit, or delete any stash.

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

## Stash fields

Each stash has a required `title`, plus optional `summary`, `content`, and `tags`. Set `isPublic` to `true` to include it in that user's public listing. The gateway assigns `ownerId`, `createdAt`, and `updatedAt`; a supplied `ownerId` must exactly match the signed-in user, and timestamps cannot be supplied by clients.

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
| `MONGO_URI` | Yes | MongoDB or Atlas connection URI. |
| `MONGO_DB` | Yes | Database name. |
| `JWT_SECRET` | Yes | Long random secret used to sign user sessions. |
| `ALLOWED_ORIGINS` | Production | Comma-separated browser origins, such as `https://app.example.com`. Defaults to `*`. |
| `PORT` | No | Listener port. Cloud providers set this automatically. |

For Atlas, configure network access for the deployment provider. Use a provider-supported static outbound address when available; do not assume there is one universal egress range for every plan.

### Local container

```sh
docker build -t m-stash .
docker run --rm -p 4000:4000 \
  -e MONGO_URI='mongodb+srv://...' \
  -e MONGO_DB='app_db' \
  -e JWT_SECRET='replace-with-a-long-random-secret' \
  -e ALLOWED_ORIGINS='http://localhost:3000' \
  m-stash
```

Check readiness with `curl http://localhost:4000/healthz`.

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

The included [render.yaml](render.yaml) is a Render Blueprint. Push the repository to a Git provider, select **New** then **Blueprint**, and connect the repository. Before creating the service, set `MONGO_URI` and `ALLOWED_ORIGINS`; Render generates `JWT_SECRET`, builds the Docker image, and verifies `/healthz`.

### Vercel

Vercel Functions cannot host persistent WebSocket connections or this long-lived server. Deploy the gateway to Railway, Fly.io, or Render, then use its public URL from a Vercel frontend.

## API

All database operations use `POST /v1/db/{collection}/{action}` and a JWT bearer token. Supported actions are `find`, `findOne`, `insertOne`, `updateOne`, and `deleteOne`.

```json
{
  "query": { "status": "active" },
  "payload": { "title": "Example" }
}
```

Persistent clients can use `wss://your-gateway.example/v1/ws/{collection}` with the same bearer token during the upgrade. Send the same JSON plus an `action` field.

Public clients use `GET /v1/public/profiles/{handle}` and `GET /v1/public/profiles/{handle}/stashes` without a token. The complete deploy-to-profile-and-stash walkthrough is in [quickstart.md](quickstart.md).

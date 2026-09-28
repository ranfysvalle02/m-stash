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

### Docker Hub

Published releases are available for `linux/amd64` and `linux/arm64`:

```sh
docker pull YOUR_DOCKERHUB_USERNAME/m-stash:latest
docker run --rm -p 4000:4000 \
  -e MONGO_URI='mongodb://user:password@mongo:27017/?authSource=admin' \
  -e MONGO_DB='app_db' \
  -e JWT_SECRET='replace-with-a-long-random-secret' \
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
  -e ALLOWED_ORIGINS='http://localhost:3000' \
  m-stash
```

Check readiness with `curl http://localhost:4000/healthz`.

### Docker Compose

The included [compose.yaml](compose.yaml) starts MongoDB and the gateway together. Copy the example environment file, replace all placeholders, then start the stack:

```sh
cp .env.example .env
docker compose up --build --wait
curl http://localhost:4000/readyz
```

To use a published image instead of building locally, replace `build: .` and `image: m-stash:local` in [compose.yaml](compose.yaml) with `image: YOUR_DOCKERHUB_USERNAME/m-stash:1.0.0`.

For custom collection rules, mount a policy file at `/etc/m-stash/gateway.json`. The image sets `M_STASH_CONFIG` to that location. Keep `JWT_SECRET` and database credentials in environment variables or your secret manager rather than the policy file.

### Kubernetes

[k8s/m-stash.yaml](k8s/m-stash.yaml) contains a two-replica Deployment, ClusterIP Service, health probes, resource settings, a read-only ConfigMap-mounted policy, and restricted pod security settings. Before applying it:

1. Replace `YOUR_DOCKERHUB_USERNAME/m-stash:latest` with a pinned published tag.
2. Set the allowed origin and collection rules in the ConfigMap.
3. Create `m-stash-secrets` from your secret manager or use [k8s/m-stash-secrets.example.yaml](k8s/m-stash-secrets.example.yaml) as a local template. Do not commit real values.

```sh
kubectl apply -f k8s/m-stash.yaml
```

`/readyz` checks MongoDB and is used for readiness; `/healthz` is the liveness endpoint. The built-in brute-force limiter is local to each replica, so use an ingress, API gateway, or shared rate limiter when you need cluster-wide enforcement.

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

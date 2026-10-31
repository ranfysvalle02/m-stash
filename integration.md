# Integrate m-stash

m-stash is an ownership layer for application data. It gives a frontend two server-enforced scopes: personal records derived from the signed-in account, and shared app records owned by the deployment. Types are buckets; visibility belongs to each record. A trusted backend can mutate shared records without receiving a user's password or MongoDB access.

For a no-build browser reference, serve [examples/browser/index.html](examples/browser/index.html) from the m-stash deployment itself. It deliberately calls `location.origin`, so the API domain is fixed to the domain hosting the page. The client keeps its bearer token only in memory and demonstrates signup, login, personal resources, and authenticated shared reads.

Set the API URL and your app's publishable key once. One deployment hosts many isolated apps; every request names its app with the `X-M-Stash-App` header (or an `?app=` query parameter for anonymous browser reads).

```sh
export API_URL='https://m-stash.onrender.com'
export APP_KEY='pk_...'   # the publishable key returned by POST /v1/apps
```

Apps are created once through the platform control plane with the deployment's `PLATFORM_TOKEN`:

```sh
curl --request POST "$API_URL/v1/apps" \
  --header "Authorization: Bearer $PLATFORM_TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"slug":"my-app","displayName":"My App","adminEmail":"admin@example.com","adminPassword":"a-long-unique-password"}'
```

The response returns the app record (with its `publishableKey`) and a one-time `secretKey` for trusted backends.

## Register users from a frontend

Registration is intentionally open. Call it from your signup flow with the email, password, and username your product collected.

```sh
curl --request POST "$API_URL/v1/auth/signup" \
  --header "X-M-Stash-App: $APP_KEY" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"a-long-unique-password","username":"ada"}'
```

The response includes a bearer `token`, a `user`, and a personal `namespace`. The same call also establishes an HttpOnly browser session when called from the m-stash origin.

Usernames are normalized to lowercase and must be 3-32 ASCII characters, unique within the app. They start and end with a letter or number, with single hyphens or underscores allowed between name parts. A username is permanent once claimed.

Use normal login to restore a session:

```sh
curl --request POST "$API_URL/v1/auth/login" \
  --header "X-M-Stash-App: $APP_KEY" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"a-long-unique-password"}'
```

For a separate browser origin, configure `ALLOWED_ORIGINS` (globally or per app) with the exact origin before production use. The current blank setting permits all origins for development but does not enable credentialed cross-origin cookies.

## Personal namespace API

Personal resources are automatically scoped to the authenticated caller. Do not submit an owner ID or namespace ID.

```sh
export TOKEN='token from signup or login'

curl --request POST "$API_URL/v1/me/resources/profile" \
  --header "X-M-Stash-App: $APP_KEY" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{
    "slug":"main",
    "title":"Ada",
    "summary":"Application profile",
    "content":"# Ada",
    "data":{"theme":"sunset"},
    "visibility":"private"
  }'
```

Use any valid type: `profile`, `preference`, `save`, `submission`, `portfolio`, or a product-specific term. Types are 3-32 lowercase letters, numbers, and hyphens; slugs are 3-64 characters using the same alphabet.

```text
GET    /v1/me/namespace
PUT    /v1/me/namespace
GET    /v1/me/resources/{type}?limit=20
GET    /v1/me/resources/{type}/{slug}
PUT    /v1/me/resources/{type}/{slug}
PATCH  /v1/me/resources/{type}/{slug}
DELETE /v1/me/resources/{type}/{slug}
```

Personal visibility is `private` or `public`. Public records resolve at `/{username}/{type}/{slug}` only if the personal namespace is public too.

## Shared application API

Each app has one shared scope for records the application owns: app name and version, configuration, feature flags, catalogs, leaderboards, announcements, and precomputed results. Types are reusable buckets; an administrator can keep one record private while publishing another in the same bucket.

```text
GET    /v1/shared/namespace
GET    /v1/shared/resources/{type}?limit=20
GET    /v1/shared/resources/{type}/{slug}
POST   /v1/shared/resources/{type}
PUT    /v1/shared/resources/{type}/{slug}
PATCH  /v1/shared/resources/{type}/{slug}
DELETE /v1/shared/resources/{type}/{slug}
```

Every authenticated user may read shared records marked `authenticated` or `public`. Administrators may read all shared records and write any of them. Writes can also use the app's `secretKey` (`export APP_SECRET='sk_...'`):

```sh
curl --request PUT "$API_URL/v1/shared/resources/leaderboard/weekly" \
  --header "X-M-Stash-App: $APP_KEY" \
  --header "Authorization: Bearer $APP_SECRET" \
  --header 'Content-Type: application/json' \
  --data '{
    "title":"Weekly leaderboard",
    "summary":"Server-computed scores",
    "content":"Updated by the scoring worker.",
    "data":{"entries":[{"username":"ada","score":9001}]},
    "visibility":"authenticated"
  }'
```

The app secret key is mutation-only: it cannot read shared resources, call personal routes, or act as a user. Keep it only in trusted backend infrastructure.

### Atomic increment

`PATCH` applies a race-free `$inc` to a single numeric `data.*` field, so concurrent writers never lose updates the way read-modify-write through `PUT` does. The field path must be under `data.`, and `by` may be any integer or float (negative to decrement). The updated resource is returned.

```sh
curl --request PATCH "$API_URL/v1/me/resources/save/slot-one" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"op":"increment","field":"data.score","by":10}'
```

The field is created from the delta if absent; incrementing a non-numeric field returns `409`.

### Querying, search, and count

List endpoints (`GET /v1/me/resources/{type}` and the shared equivalent) accept optional query parameters that always compose with — and can only narrow — the ownership and visibility scope.

- `?filter=<json>` — a safe MQL subset. Allowed operators are `$eq $ne $gt $gte $lt $lte $in $nin $and $or $not`; fields are limited to `data.*` plus `slug`, `visibility`, `createdAt`, and `updatedAt`. Timestamps use RFC3339 strings. Anything else (`$where`, `$regex`, `$lookup`, raw sub-documents, other fields) is rejected.
- `?q=<text>` — full-text search over `title`, `summary`, and `content`, ranked by relevance; each result carries a `score`.
- `?count=1` — returns `{"data":{"count":N}}` for the current scope and filter instead of a page.

```sh
curl --get "$API_URL/v1/shared/resources/leaderboard" \
  --header "Authorization: Bearer $TOKEN" \
  --data-urlencode 'filter={"data.score":{"$gte":1000}}'

curl --get "$API_URL/v1/shared/resources/announcement" \
  --header "Authorization: Bearer $TOKEN" \
  --data-urlencode 'q=release notes'
```

### Webhooks

When the deployment runs with `WEBHOOK_DELIVERY=true`, a worker drains the transactional outbox and delivers each event to the owning app's registered endpoints. Registration is admin-only (`/v1/shared/webhooks`) and scoped to the app; the signing secret is returned exactly once at creation.

```sh
curl --request POST "$API_URL/v1/shared/webhooks" \
  --header "X-M-Stash-App: $APP_KEY" \
  --header "Authorization: Bearer $ADMIN_TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"url":"https://example.com/hook","eventTypes":["m-stash.resources"]}'
```

`eventTypes` entries match by exact type (`m-stash.resources.increment.v1`), dotted prefix (`m-stash.resources`), or `*`. Each delivery is a `POST` of the event JSON with `X-M-Stash-Event-Id`, `X-M-Stash-Event-Type`, and an HMAC-SHA256 `X-M-Stash-Signature: sha256=<hex>` over the raw body. Delivery is at-least-once (dedupe on the event id); failures retry with exponential backoff and dead-letter after `WEBHOOK_MAX_ATTEMPTS`.

Shared visibility has three values:

| Value | Who can read it |
| --- | --- |
| `private` | App administrators only. |
| `authenticated` | Any signed-in user. |
| `public` | Anyone via `/v1/public/shared/resources/{type}/{slug}`. |

## Public routes

The embedded UI exposes public app data without a username at `/public`. Browse a typed shared-data bucket at `/public/{type}` and an individual public record at `/public/{type}/{slug}`. An administrator creates the bucket by using a shared resource type, then chooses `public` visibility for each record that should appear there.

Anonymous API reads still need to name the app. Send the `X-M-Stash-App` header, or add `?app=$APP_KEY` to the URL for plain browser calls that cannot set headers:

```sh
curl "$API_URL/v1/public/ada/profile/main" --header "X-M-Stash-App: $APP_KEY"
curl "$API_URL/v1/public/shared/resources/announcement/release-notes?app=$APP_KEY"
```

Public collection endpoints use cursor pagination. Pass `page.nextCursor` as `cursor`; limits range from 1 through 100.

## Verify and operate

```sh
curl "$API_URL/v1/auth/verify" --header "X-M-Stash-App: $APP_KEY" --header "Authorization: Bearer $TOKEN"
curl "$API_URL/.well-known/m-stash.json"
curl "$API_URL/healthz"
curl "$API_URL/readyz"
```

Every signup and mutation uses a MongoDB transaction and writes a durable outbox event in the same commit. Run MongoDB as a replica set or sharded cluster.

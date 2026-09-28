# Integrate m-stash

m-stash is a reusable identity and scoped-data API. A frontend can register users, give each one an immutable username, store profile-like records in their personal namespace, and read deployment-controlled shared data. A trusted backend can mutate shared state without receiving a user's password or MongoDB access.

Set the API URL once:

```sh
export API_URL='https://m-stash.onrender.com'
```

## Register users from a frontend

Registration is intentionally open. Call it from your signup flow with the email, password, and username your product collected.

```sh
curl --request POST "$API_URL/v1/auth/signup" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"a-long-unique-password","username":"ada"}'
```

The response includes a bearer `token`, a `user`, and a personal `namespace`. The same call also establishes an HttpOnly browser session when called from the m-stash origin.

Use normal login to restore a session:

```sh
curl --request POST "$API_URL/v1/auth/login" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"a-long-unique-password"}'
```

For a separate browser origin, configure `ALLOWED_ORIGINS` with the exact origin before production use. The current blank setting permits all origins for development but does not enable credentialed cross-origin cookies.

## Personal namespace API

Personal resources are automatically scoped to the authenticated caller. Do not submit an owner ID or namespace ID.

```sh
export TOKEN='token from signup or login'

curl --request POST "$API_URL/v1/me/resources/profile" \
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
DELETE /v1/me/resources/{type}/{slug}
```

Personal visibility is `private` or `public`. Public records resolve at `/{username}/{type}/{slug}` only if the personal namespace is public too.

## Shared application API

Each deployment has one shared control-plane scope. It is appropriate for server-owned or deployment-owned data including configuration, leaderboards, inventory catalogs, published announcements, and precomputed results.

```text
GET    /v1/shared/namespace
GET    /v1/shared/resources/{type}?limit=20
GET    /v1/shared/resources/{type}/{slug}
POST   /v1/shared/resources/{type}
PUT    /v1/shared/resources/{type}/{slug}
DELETE /v1/shared/resources/{type}/{slug}
```

Every authenticated user may read shared records marked `authenticated` or `public`. Administrators may read all shared records and write any of them. Writes can also use the deployment's `SERVICE_TOKEN`:

```sh
curl --request PUT "$API_URL/v1/shared/resources/leaderboard/weekly" \
  --header "Authorization: Bearer $SERVICE_TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{
    "title":"Weekly leaderboard",
    "summary":"Server-computed scores",
    "content":"Updated by the scoring worker.",
    "data":{"entries":[{"username":"ada","score":9001}]},
    "visibility":"authenticated"
  }'
```

`SERVICE_TOKEN` is mutation-only: it cannot read shared resources, call personal routes, or act as a user. Keep it only in trusted backend infrastructure.

Shared visibility has three values:

| Value | Who can read it |
| --- | --- |
| `private` | Deployment administrators only. |
| `authenticated` | Any signed-in user. |
| `public` | Anyone via `/v1/public/shared/resources/{type}/{slug}`. |

## Public routes

```sh
curl "$API_URL/v1/public/ada/profile/main"
curl "$API_URL/v1/public/shared/resources/announcement/release-notes"
```

Public collection endpoints use cursor pagination. Pass `page.nextCursor` as `cursor`; limits range from 1 through 100.

## Verify and operate

```sh
curl "$API_URL/v1/auth/verify" --header "Authorization: Bearer $TOKEN"
curl "$API_URL/.well-known/m-stash.json"
curl "$API_URL/healthz"
curl "$API_URL/readyz"
```

Every signup and mutation uses a MongoDB transaction and writes a durable outbox event in the same commit. Run MongoDB as a replica set or sharded cluster.

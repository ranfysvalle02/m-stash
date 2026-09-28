# m-stash Quickstart

This path takes a fresh MongoDB deployment to a secure, policy-scoped application API. The included `stashes` collection is only a sample surface for verifying the flow; define collection rules for the data model your application actually needs.

## 1. Deploy on Render

1. Push this repository to GitHub, GitLab, or Bitbucket.
2. In Render, choose **New** then **Blueprint** and select the repository. Render reads [render.yaml](render.yaml), builds the Docker image, waits for `/healthz`, and creates an HTTPS service.
3. Set `MONGO_URI` to an Atlas or self-hosted **replica set or sharded** connection URI. Set `MONGO_DB` if `app_db` is not the intended database. Configure `ADMIN_EMAIL` and `ADMIN_PASSWORD` together to bootstrap the first administrator. Set `ALLOWED_ORIGINS` to the exact origin of any separate browser application. Render generates `JWT_SECRET` and `METRICS_TOKEN`; the Blueprint enables secure cookies and trusted proxy handling.
4. Copy the public service URL and use it below as `API_URL`.

For Atlas, allow network access from Render according to the capabilities of your plan. Use a Render Web Service, not a Static Site, so the HTTP API and WebSocket endpoint remain available.

## 2. Verify the operator flow

Open `API_URL` in a browser and sign in with the configured administrator, or create a normal account. The embedded operator workspace lets you verify the included collection and inspect policy-limited queries. A profile is optional; public sharing is always explicit.

## 3. Create a user and token

Set the service URL without a trailing slash:

```sh
export API_URL='https://your-service.onrender.com'
```

Create an account:

```sh
curl --request POST "$API_URL/v1/auth/signup" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"use-a-long-unique-password"}'
```

The response contains a `token` and `user.id`. Export them before making authenticated API requests:

```sh
export TOKEN='paste-the-token-from-the-signup-response'
export USER_ID='paste-the-user.id-from-the-signup-response'
```

To obtain a token later, call `POST /v1/auth/login` with the same credentials. Browser clients can instead use the same-origin session cookies issued by signup and login.

## 4. Verify scoped access with the sample collection

Create a private record. The policy hydrates `ownerId` from the authenticated user; do not supply `ownerId`, `createdAt`, or `updatedAt`.

```sh
curl --request POST "$API_URL/v1/db/stashes/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"title":"Private sample record","summary":"Verifies the scoped data path.","content":"Only the owner may change this record.","isPublic":false}}'
```

Query the records visible to the current identity. The gateway combines this query with the collection's read rule before sending it to MongoDB:

```sh
curl --request POST "$API_URL/v1/db/stashes/find" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"query":{}}'
```

The `stashes` endpoint is the included sample collection, not a required application model. Add named rules for collections such as `inventory`, `playerProfiles`, or `operationalState` before your application accesses them.

## 5. Optional public items

Create an explicitly shared item by setting `isPublic` to `true`:

```sh
curl --request POST "$API_URL/v1/db/stashes/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"title":"Public sample item","summary":"An intentionally shared record.","content":"Only public fields are exposed through the named public routes.","tags":["intro"],"isPublic":true}}'
```

Optional public profiles use an explicit allowlisted projection. Create one with the dedicated authenticated endpoint:

```sh
curl --request PUT "$API_URL/v1/me/profile" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"handle":"ada-lovelace","displayName":"Ada Lovelace","bio":"Mathematician and programmer.","avatarURL":"https://example.com/ada.jpg","links":[{"label":"Website","url":"https://example.com"}],"isPublic":true}'
```

Anyone can read only the allowlisted public profile and shared items, without a token:

```sh
curl "$API_URL/v1/public/profiles/ada-lovelace"
curl "$API_URL/v1/public/profiles/ada-lovelace/stashes?limit=20&tag=intro"
curl "$API_URL/v1/public/stashes?limit=20&tag=intro"
```

Public feeds accept only `limit`, `cursor`, and `tag`. They do not expose arbitrary MongoDB filters. Use `page.nextCursor` with the same filters to request another page.

## 6. Optional WebSocket connection

Connect to `wss://your-service.onrender.com/v1/ws/stashes` with the `Authorization: Bearer <token>` upgrade header. Send the same action shape used by the HTTP API:

```json
{
  "action": "find",
  "query": {}
}
```

Each message is evaluated against the same collection rule and supports `find`, `findOne`, `insertOne`, `updateOne`, and `deleteOne`.

## Next step

Add your application collections to `gateway.json` or the file selected by `M_STASH_CONFIG`. Give clients only the fields and document scope they should control. Keep authoritative outcomes, credentials, and direct MongoDB access in trusted services. Keep `MONGO_URI` and `JWT_SECRET` in your platform's secret manager, never in browser code.
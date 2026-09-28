# m-stash API Quickstart

This path takes a fresh MongoDB database to a deployed API with private and public stashes. A profile stays owner-editable while a small, projection-safe representation and its public stashes are available to anyone by handle.

## 1. Deploy on Render

1. Push this repository to GitHub, GitLab, or Bitbucket.
2. In Render, choose **New** then **Blueprint** and select the repository. Render reads [render.yaml](render.yaml), builds the Docker image, waits for `/healthz`, and creates an HTTPS service.
3. Before creating the Blueprint, enter `MONGO_URI` as your Atlas or self-hosted **replica set or sharded** MongoDB connection URI. Set `ALLOWED_ORIGINS` to the URL of your browser app, such as `https://app.example.com`. Configure `ADMIN_EMAIL` and `ADMIN_PASSWORD` together to bootstrap the first administrator; m-stash creates that account, or promotes a matching existing account, on startup. `MONGO_DB` defaults to `app_db`; Render generates `JWT_SECRET` and `METRICS_TOKEN`.
4. Copy the public service URL from Render and use it below as `API_URL`.

For MongoDB Atlas, configure network access for the Render service. Use a Render static outbound address when your plan provides one; otherwise follow Render and Atlas guidance for your plan rather than assuming a fixed shared range. Use a Render Web Service rather than a Static Site so HTTPS and WebSockets remain available.

## 2. Open the creator workspace

Open `API_URL` in a browser to use the embedded workspace. The initial signup flow creates a profile, lets you write a Markdown post, and lets you publish it without exposing database credentials in the browser. The optional `ADMIN_EMAIL` account uses the same login flow.

## 3. Create a user

Set your service URL without a trailing slash:

```sh
export API_URL='https://your-service.onrender.com'
```

Create an account and retain its session token:

```sh
curl --request POST "$API_URL/v1/auth/signup" \
  --header 'Content-Type: application/json' \
  --data '{"email":"ada@example.com","password":"use-a-long-unique-password"}'
```

The response contains a `token` and `user.id`. Export both before making database requests:

```sh
export TOKEN='paste-the-token-from-the-signup-response'
export USER_ID='paste-the-user.id-from-the-signup-response'
```

To obtain a new token later, call `POST /v1/auth/login` with the same email and password.

## 4. Publish a public profile

Create the profile. Do not send `_id`: the gateway derives it from the signed-in user. A public handle uses 3-32 lowercase letters, numbers, hyphens, or underscores.

```sh
curl --request POST "$API_URL/v1/db/profiles/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"handle":"ada-lovelace","displayName":"Ada Lovelace","bio":"Mathematician and programmer.","avatarURL":"https://example.com/ada.jpg","links":[{"label":"Website","url":"https://example.com"}],"isPublic":true}}'
```

Anyone can now read the public representation without a token:

```sh
curl "$API_URL/v1/public/profiles/ada-lovelace"
```

The public response contains only `handle`, `displayName`, `bio`, `avatarURL`, and `links`; it does not expose every field stored in the profile document.

## 5. Create private and public stashes

Create a private stash. Omit `ownerId`, `createdAt`, and `updatedAt`: the gateway supplies them. A supplied `ownerId` is accepted only when it matches the signed-in user; timestamps are rejected.

```sh
curl --request POST "$API_URL/v1/db/stashes/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"title":"Private draft","content":"Only the owner can read or edit this.","isPublic":false}}'
```

Publish a second stash by setting `isPublic` to `true`:

```sh
curl --request POST "$API_URL/v1/db/stashes/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"title":"Hello, public web","summary":"A visible post.","content":"This is shared intentionally.","tags":["intro"],"isPublic":true}}'
```

List only the signed-in user's stashes, including private drafts. `ownerId` is normalized to a MongoDB ObjectID by the gateway:

```sh
curl --request POST "$API_URL/v1/db/stashes/find" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"query":{"ownerId":"'"$USER_ID"'"}}'
```

An authenticated `find` with an empty query returns the caller's stashes plus other users' public stashes. Use the public route below for an anonymous profile page.

Anyone can list only the user's public stashes, with no token. Results are cursor-paginated (20 items by default, up to 100) and can be filtered by one exact tag:

```sh
curl "$API_URL/v1/public/profiles/ada-lovelace/stashes?limit=20&tag=intro"
```

The response includes `page.nextCursor`. Pass it as `cursor` with the same filters to retrieve the next page:

```sh
curl "$API_URL/v1/public/profiles/ada-lovelace/stashes?limit=20&tag=intro&cursor=PASTE_NEXT_CURSOR"
```

Visitors receive only public fields. They cannot call the authenticated write routes, and the owner policy means they cannot edit another person's stash.

## Global discovery

Published stashes can also appear in the global discovery feed. It supports the same cursor pagination and exact tag filter, but returns compact previews rather than full content:

```sh
curl "$API_URL/v1/public/stashes?limit=20&tag=intro"
```

Open a preview by `_id` to load its full public content:

```sh
curl "$API_URL/v1/public/stashes/PUBLIC_STASH_ID"
```

The global feed accepts only `limit`, `cursor`, and `tag`; it deliberately does not expose arbitrary database filters.

## 6. Optional WebSocket connection

Connect to `wss://your-service.onrender.com/v1/ws/stashes` with the `Authorization: Bearer <token>` upgrade header. Send a text message such as:

```json
{
  "action": "find",
  "query": { "isPublic": false }
}
```

Each WebSocket message uses the same JSON shape, security policy, and supported actions as the HTTP API: `find`, `findOne`, `insertOne`, `updateOne`, and `deleteOne`.

## Next step

The default rules cover owner-only `profiles` and owner-managed `stashes`. Add collection policies in `gateway.json` before exposing additional MongoDB collections. Create named public routes with explicit filters and field projections rather than making generic collections publicly readable. Keep `MONGO_URI` and `JWT_SECRET` only in Render's environment configuration; never put them in browser code.
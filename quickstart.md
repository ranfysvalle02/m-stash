# m-stash Quickstart

Deploy m-stash once to give an application two server-enforced data scopes: personal records for signed-in users and shared app records for the deployment. This guide uses Render, but the runtime contract is ordinary environment configuration plus a transactional MongoDB deployment.

## 1. Provision the deployment

1. Push this repository to a Git provider.
2. In Render, choose **New**, then **Blueprint**, and select the repository. Render reads [render.yaml](render.yaml) and builds the Docker image.
3. Set `MONGO_URI` to an Atlas or self-hosted **replica set or sharded** URI. Set `MONGO_DB` when `app_db` is not appropriate.
4. Set a 32-character-or-longer `JWT_SECRET` when your platform does not generate it.
5. Set `ADMIN_EMAIL` and `ADMIN_PASSWORD` together to create the deployment administrator. This account controls shared data; it does not need or receive a username.
6. Optionally set `SERVICE_TOKEN` for a trusted API, game server, worker, or webhook processor that must write shared data.

Render sets secure cookies and trusted-proxy behavior for its HTTPS edge. Configure exact `ALLOWED_ORIGINS` before using browser authentication from a different origin.

## 2. Register application users

Your frontend calls the open registration endpoint. The username is immutable because it is both a personal route and a stable ownership key.

```sh
export API_URL='https://your-service.onrender.com'

curl --request POST "$API_URL/v1/auth/signup" \
  --header 'Content-Type: application/json' \
  --data '{"email":"player@example.com","password":"use-a-long-unique-password","username":"player-one"}'
```

The response contains a `token`, `user`, and `namespace`. Use the token in native or server clients:

```sh
export TOKEN='paste-the-token-from-the-signup-response'
```

Browser signup and login also issue HttpOnly session cookies. Never expose MongoDB credentials, `JWT_SECRET`, an admin password, or `SERVICE_TOKEN` to browser code.

## 3. Store personal data

Personal routes are ideal for profiles, preferences, loadouts, save files, submissions, or public work. The server assigns namespace and creator identity from the token.

```sh
curl --request POST "$API_URL/v1/me/resources/profile" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{
    "slug":"main",
    "title":"Player one",
    "summary":"Public player profile",
    "content":"# Player one",
    "data":{"favoriteMode":"arcade"},
    "visibility":"public"
  }'
```

The public route is now `https://your-service.onrender.com/player-one/profile/main`.

## 4. Store shared application data

The deployment's shared app scope holds data the application controls: app name, version, configuration, feature flags, catalogs, and leaderboards. Types are buckets; visibility belongs to each record. Signed-in users can read `authenticated` records, but only admins or a trusted backend can write them.

```sh
export SERVICE_TOKEN='store-this-only-in-your-backend'

curl --request POST "$API_URL/v1/shared/resources/leaderboard" \
  --header "Authorization: Bearer $SERVICE_TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{
    "slug":"weekly",
    "title":"Weekly leaderboard",
    "summary":"Server-computed ranking",
    "content":"Updated by the scoring service.",
    "data":{"entries":[{"username":"player-one","score":1240}]},
    "visibility":"authenticated"
  }'
```

The service token can mutate only shared resources. It cannot read data or impersonate a user. Use an administrator's normal email/password session for the embedded shared-data workspace.

## 5. Verify the service

```sh
curl "$API_URL/.well-known/m-stash.json"
curl "$API_URL/healthz"
curl "$API_URL/readyz"
```

For complete API shapes and visibility rules, read [integration.md](integration.md).

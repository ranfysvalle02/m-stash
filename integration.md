# Integrate An Application

Use `https://m-stash.onrender.com` as the API base URL for the application owned by this m-stash deployment. m-stash is an application gateway, not a self-service multi-tenant database: independently operated applications need their own m-stash deployment, MongoDB database, `JWT_SECRET`, and collection policy.

## 1. Prepare the gateway

The service owner must complete these Render settings before connecting a separate browser application:

1. In **Render > m-stash > Environment**, set `ALLOWED_ORIGINS` to the exact browser origin, for example `https://app.example.com`. Use a comma-separated list for multiple known origins.
2. Redeploy the service after changing an environment variable.
3. Keep `SESSION_COOKIE_SECURE=true` and `TRUST_PROXY=true` for Render HTTPS.

The deployed service already permits the bundled `profiles` and `stashes` collections. Do not use `stashes` as an application model unless its owner-scoped fields are what your application needs.

For a new collection, the service owner must add a deployment-owned policy and rebuild the Docker image. There is no API that lets a client change policy at runtime. For example, create `gateway.json`:

```json
{
  "rules": {
    "tasks": {
      "read": { "ownerId": "$auth.uid" },
      "write": { "ownerId": "$auth.uid" },
      "allowed_write_fields": ["ownerId", "title", "completed"],
      "restricted_write_fields": ["role", "createdAt"]
    }
  }
}
```

Then include it in the image before deployment:

```dockerfile
COPY gateway.json /etc/m-stash/gateway.json
```

The Docker image already uses `/etc/m-stash/gateway.json` as `M_STASH_CONFIG`. A policy file replaces the built-in rule set, so include the `profiles` or `stashes` rules too when the application still needs them.

## 2. Register or sign in a user

Set the API base URL once:

```sh
export API_URL='https://m-stash.onrender.com'
```

Create an account, or call `/v1/auth/login` with the same body for an existing account:

```sh
curl --request POST "$API_URL/v1/auth/signup" \
  --header 'Content-Type: application/json' \
  --data '{"email":"person@example.com","password":"use-a-long-unique-password"}'
```

The response contains `token` and `user`. Send the token from a trusted server, native app, or other bearer-token client:

```sh
export TOKEN='token from the signup or login response'
```

For a browser hosted on the same origin as m-stash, signup and login also issue `HttpOnly` session cookies. Do not expose `MONGO_URI`, `JWT_SECRET`, an admin password, or a privileged service token to browser code.

## 3. Make an authenticated data request

Every protected request is a `POST` to:

```text
/v1/db/{collection}/{action}
```

Supported actions are `find`, `findOne`, `insertOne`, `updateOne`, and `deleteOne`. This sample uses the bundled owner-scoped `stashes` collection:

```sh
curl --request POST "$API_URL/v1/db/stashes/insertOne" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"payload":{"title":"First record","summary":"Created through m-stash","isPublic":false}}'
```

Read only the records the current policy permits:

```sh
curl --request POST "$API_URL/v1/db/stashes/find" \
  --header "Authorization: Bearer $TOKEN" \
  --header 'Content-Type: application/json' \
  --data '{"query":{},"limit":20}'
```

The gateway adds the collection policy to every query and write. For owner-scoped rules, omit `ownerId` on inserts: m-stash writes it from the authenticated user. Do not treat client-supplied filters as authorization.

## 4. Verify and operate

Confirm a token for a downstream service without sharing `JWT_SECRET`:

```sh
curl "$API_URL/v1/auth/verify" \
  --header "Authorization: Bearer $TOKEN"
```

Check the service contract and health during setup or monitoring:

```sh
curl "$API_URL/.well-known/m-stash.json"
curl "$API_URL/healthz"
curl "$API_URL/readyz"
```

Use `X-Request-ID` from every response when investigating a request. The optional WebSocket surface is `wss://m-stash.onrender.com/v1/ws/{collection}` and accepts the same bearer token plus the same action body as the HTTP API.

## 5. Add public data only deliberately

For the bundled sample collection, set `isPublic: true` only for records intended for anyone to read. Public discovery is separate from the database proxy:

```text
GET https://m-stash.onrender.com/v1/public/stashes?limit=20
```

Custom public routes should use purpose-built allowlisted projections. Do not make a private collection public by setting a broad proxy rule.
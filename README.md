# m-stash

----

What makes this project explosive as an open-source tool is that it restores the "backend-less" superpower MongoDB stripped away in late 2025, giving MongoDB developers the instant, client-facing experience PostgreSQL users enjoy with Supabase. It allows a React, Vue, Svelte, or mobile app to communicate directly with any MongoDB deployment (Atlas or self-hosted) without writing repetitive Express API routes, while ensuring bulletproof, document-level security.

### The Coolest Features

* **Seamless AST Query Rewriting (DLAC Engine):** Frontend developers can issue standard MongoDB queries like `db.collection('portfolios').find({ category: 'tech' })`. The gateway intercepts the request, parses the user's JWT, and dynamically rewrites the query tree into `{ $and: [{ category: 'tech' }, { ownerId: 'user_123' }] }` before forwarding it to MongoDB. Bypassing security from the browser is mathematically impossible because enforcement happens at the proxy layer.
* **Context-Aware BSON ObjectID Normalization:** A notorious MongoDB pain point is query failure when a frontend client sends `"_id": "60d5..."` as a string instead of `ObjectID("60d5...")`. The proxy recursively walks the AST—even through complex operators like `{"_id": {"$in": ["60d5...", "60d6..."]}}`—and automatically casts 24-character hex strings to `ObjectID`s when targeting identifier fields. Regular 24-character hex strings (like Git commit SHAs or API keys) are preserved as strings.
* **Self-Hydrating Inserts & Field-Mask Safeguards:** When a user creates a new record, the proxy automatically hydrates identity fields mandated by security policy (e.g., auto-attaching `ownerId: claims.UID`). If a malicious client attempts to spoof their `ownerId` or write to restricted schema fields like `role` or `isVerified`, the gateway rejects the payload with a `403 Forbidden`.
* **Native Go Concurrency in a ~15MB Binary:** Unlike Node.js proxies that carry multi-hundred-megabyte `node_modules` folders, heavy idle RAM usage, and single-threaded event loop bottlenecks, this Go engine compiles into a single static binary. It handles thousands of concurrent database requests using lightweight goroutines while idling at under 15MB RAM—meaning it can run permanently on free-tier containers on Fly.io, Railway, or Render.
* **Zero DSL Policy Configuration (`gateway.json`):** Instead of forcing developers to learn a custom policy language (like Rego, Cedar, or AWS IAM syntax), security policies are written using standard MongoDB query syntax. If a developer knows how to query MongoDB, they already know how to write access rules.
* **Embedded Auth & Storage:** Out-of-the-box password hashing (bcrypt) and session management (`/v1/auth/signup`, `/v1/auth/login`) store user accounts directly inside a `_users` collection in your existing Mongo database, making the gateway completely self-contained without needing third-party auth vendors.

-----

When software architects treat Authentication (AuthN) and Authorization (AuthZ) as separate silos, they recreate the exact middleware friction this proxy is built to eliminate.

```
┌────────────────────────────────────────────────────────────────────────┐
│                        FULL-STACK AUTH ENGINE                          │
│                                                                        │
│   1. Authentication (AuthN)            2. Authorization (AuthZ)        │
│   "WHO ARE YOU?"                       "WHAT CAN YOU TOUCH?"           │
│   ┌────────────────────────┐           ┌───────────────────────────┐   │
│   │ • Signup / Login       │ ──JWT──►  │ • AST Query Rewriter      │   │
│   │ • Password Hashing     │  Claims   │ • $auth.uid Injection     │   │
│   │ • JWT Minting & Claims │           │ • Field-Level Write Masks │   │
│   └────────────────────────┘           └───────────────────────────┘   │
└────────────────────────────────────────────────────────────────────────┘

```

---

### The Difference: AuthN vs. AuthZ

| Layer | Responsibility | What it Handles in the Go Gateway |
| --- | --- | --- |
| **Authentication (AuthN)** | **Identity Verification** ("Who are you?") | `/v1/auth/signup`, `/v1/auth/login`, bcrypt password hashing, JWT minting, session validation, user record creation in `_users`. |
| **Authorization (AuthZ)** | **Permission & Data Isolation** ("What can you read/write?") | Dynamic AST query rewriter, injecting claims (`$auth.uid`) into BSON queries, field-level write masks, collection rule enforcement. |

---

### Why Combining Them into "One System" is the Strategic Win

If you only build an Authorization Proxy, developers still have to plug in Auth0, Clerk, or Firebase Auth to handle users. That creates three massive architectural problems:

#### 1. Identity Fragmentation (The Sync Problem)

When AuthN lives in Auth0 and database data lives in MongoDB Atlas, user identities become fragmented. You end up maintaining a shadow user table in MongoDB to link Auth0 `user_id`s with internal document relationships. By embedding AuthN directly inside the gateway, user identity lives right inside MongoDB's `_users` collection natively.

#### 2. The Token Hydration Loop

AuthN and AuthZ feed off each other in real-time. During login, the AuthN engine mints a JWT packed with custom claims:

```json
{
  "uid": "usr_99",
  "email": "alex@startup.com",
  "role": "org_admin"
}

```

The AuthZ rewriter engine instantly consumes those claims to hydrate MongoDB rules dynamically:

```json
// Rule Policy
{ "tenantId": "$auth.role", "ownerId": "$auth.uid" }

// Rewritten BSON Query sent to Atlas
{ "tenantId": "org_admin", "ownerId": "usr_99" }

```

When AuthN and AuthZ share the same binary, claim hydration happens instantly in memory without external token verification round-trips.

#### 3. Complete Developer Ergonomics (The "Supabase Effect")

Developers don't want to wire together three different cloud services just to build an MVP. They want **one SDK** that handles identity and database calls:

```javascript
import { createClient } from '@mongo-open-auth/client';

const db = createClient({ endpoint: 'https://api.mygateway.dev' });

// 1. Authentication (AuthN)
const { token } = await db.auth.login({ email, password });

// 2. Authorization & Database Access (AuthZ)
const myData = await db.collection('projects').find({ active: true });

```

---

### The Strategic Positioning

If you only solve **Data Access (AuthZ)**, you built a niche database proxy.

If you solve **Identity (AuthN) + Data Access (AuthZ)** together, you built **the open-source Firebase/Supabase equivalent for MongoDB**.

That combination captures developers at the exact moment they choose their auth layer, locking in MongoDB Atlas as their permanent database foundation.

# Secure, Scoped MongoDB Access for Applications

Most applications need MongoDB data in the browser without giving the browser a database password. They also need a clear answer to a harder question: which user may read or change which document and field?

`m-stash` is the service boundary for that problem. Your app authenticates with it, calls its API, and m-stash applies the configured collection policy before a request reaches MongoDB.

## One controlled path

The path is deliberately small:

1. m-stash signs users in, issues browser sessions and JWTs, and verifies incoming tokens.
2. A request targets a configured collection and action.
3. The gateway validates the client filter, merges it with the collection rule, and enforces field allowlists for writes.
4. MongoDB receives only the scoped operation.

Collections that have no rule, along with `_m_stash_*` internal collections, are unavailable through the generic API. The browser never receives the MongoDB connection string.

## Private by default, public by design

The default rules include `profiles` and `stashes`. The latter is an included sample collection for verifying the data path; it is not the product's required domain model. Applications can add rules for inventory, application state, preferences, workflow records, or any other owned data.

Private data stays behind authenticated policy rules. If an application needs a public surface, m-stash uses named endpoints with explicit filters and field projections instead of making a generic collection openly queryable. The public profile route, for example, returns only `handle`, `displayName`, `bio`, `avatarURL`, and `links`, even if the backing document gains additional private fields later.

## Built for production boundaries

Every authenticated mutation is written alongside an immutable outbox event in the same MongoDB transaction. That requires a replica set or sharded deployment, and gives downstream workers a durable, idempotent boundary for delivery and integration work. Cursor-paginated public feeds, fixed-window authentication limits, request IDs, readiness checks, structured logs, and optional protected metrics are included in the same service.

The result is not an ORM and not direct database access from a browser. It is a focused MongoDB application gateway: secure by default, scoped by policy, and ready to operate alongside the rest of your system.

For an end-to-end setup, see [quickstart.md](quickstart.md).

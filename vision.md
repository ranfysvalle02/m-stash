# Vision

## Promise

**Auth and per-user data on MongoDB, already done, that a vibe coder (or their AI) can't misconfigure.**

m-stash is the thin, opinionated layer that turns a signed-in identity into server-enforced data ownership. You never design a schema, write a security rule, or touch a database.

## Who it's for

Vibe coders and AI-app builders who need signup/login, profiles, saves, and shared app data — and want it to *just work* without becoming a backend engineer. The database is invisible.

## The core bet

Every other backend makes you re-solve "scope this data to the right user" (Supabase RLS, Firebase rules) — the part people get wrong and the part that leaks. In m-stash the insecure version is **inexpressible**: ownership is the shape of the API, not a policy you author. That is the moat and the whole product.

- One deployment hosts many isolated apps; a publishable key names the app, a secret key is backend-only.
- Two scopes: personal (owned by a user) and shared (owned by the app). Per-record visibility: private / authenticated / public.
- MongoDB is the engine, never the interface.

## Scope discipline (the product *is* the tightness)

Before adding anything, ask: **does this help someone own and scope data by identity — or does it turn m-stash into a general query engine?**

**In** — makes "already done" more true:
- Identity, ownership, visibility, personal/shared scopes
- Auth completeness: password reset, email verification, OAuth
- A tiny typed client SDK; templates; AI-tool integrations

**Out** — makes us a worse Supabase:
- Joins, relations, aggregation pipelines, raw queries, custom indexes
- Arbitrary schemas or a third scope
- Per-app UI theming, dashboards, billing (until validated)

When someone outgrows the model, the answer is "reach past us to your own Mongo" — the escape hatch is a feature, not a failure.

## Non-goals

- We do **not** compete with Supabase/Firebase on feature breadth.
- We do **not** expose MongoDB concepts (collections, ObjectIds, connection strings) to the app developer.
- We do **not** trade the "can't misconfigure" guarantee for flexibility.

## North star

Every change must preserve one sentence: *clone, set a few env vars, provision an app, ship auth + per-user data in minutes — with no way to leak another tenant's data.* If a feature weakens that sentence, it doesn't belong.

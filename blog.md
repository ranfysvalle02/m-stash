# A MongoDB Gateway for Private and Public Stashes

Most applications need both account data and data meant for the open web. Treating both as one generic collection rule makes the boundary easy to blur: a profile can start with a display name and later gain an email address, moderation state, billing metadata, or internal flags.

`m-stash` makes the boundary explicit.

## Three data boundaries

The gateway keeps account records in `_users`, owner-editable profile documents in `profiles`, owner-managed content in `stashes`, and public representations behind named read endpoints.

The account collection is gateway-managed. It contains authentication material and is never exposed through the database proxy. Profile documents are writable only by their owner, identified by the JWT. A public endpoint is available only for profiles that opt into publication with `isPublic: true`.

The profile endpoint does not return a profile document wholesale. MongoDB applies a projection that includes only `handle`, `displayName`, `bio`, `avatarURL`, and `links`. This protects future private fields by default: adding `email`, `preferences`, `moderation`, or application-specific data to a profile does not make it public.

## Stashes are the publishing primitive

A stash is a user-owned document with a required title and optional content, summary, and tags. The gateway adds the owner identity and timestamps. The owner can create, update, and delete only their own stashes through the authenticated API. A stash is private unless its owner chooses `isPublic: true`.

Public stashes are available at a profile-scoped route:

```http
GET /v1/public/profiles/ada-lovelace/stashes
```

The route first verifies that the profile is published, then returns only public stashes belonging to that profile's owner. It returns an allowlisted projection rather than the raw document. This lets a stash gain private annotations or product-specific metadata later without leaking it to readers.

## A stable external identity

A profile handle is the external identifier. The endpoint accepts a lowercase handle containing letters, digits, hyphens, or underscores, and resolves only a published profile:

```http
GET /v1/public/profiles/ada-lovelace
```

The public profile and stash list can be cached at an edge, linked from a product, or consumed by a static site without carrying a user token. The rest of each document remains controlled through the authenticated gateway.

## Policies still govern private data

The generic database API remains document-policy driven. Stashes use standard MongoDB JSON policy to combine a caller's query with an owner-or-public filter, so signed-in callers can discover public stashes while writes remain owner-only. Profiles use an owner filter for all authenticated reads and mutations.

This split gives applications a useful extension point: create new public surfaces as named routes with explicit filters and projections, while keeping richer operational data behind claims-aware rules. Public data stays intentionally small; private data remains flexible.

For an end-to-end example, see [quickstart.md](quickstart.md).

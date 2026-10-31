# One identity layer, two data planes

Most applications start with a familiar problem: a frontend needs registration, login, a profile, and somewhere to put user-owned data. Soon it also needs data the application owns: the current leaderboard, a catalog, feature configuration, moderation outcomes, or trusted game state.

A generic database API collapses those concerns into one permission problem. m-stash keeps them distinct — and it does so for many applications on one deployment. Each app is provisioned once through the platform control plane and gets a publishable key that identifies it on every request, plus a secret key for trusted backends. Every user, namespace, and record is scoped to its app, so tenants are isolated by construction and MongoDB stays invisible to the developer.

Open API registration creates a user and an immutable personal namespace atomically, within the calling app. A frontend collects email, password, and username once; the server assigns ownership on every later write. That makes profiles, saves, preferences, submissions, and public portfolios ordinary typed resources without accepting an owner ID from the browser.

App administration is separate. Provisioning an app can create its administrator account directly. The administrator controls that app's shared scope but does not need a public username. The shared scope holds application-owned records, with visibility that can be private to admins, readable to signed-in users, or deliberately public.

A trusted scoring service can update a shared leaderboard using the app's secret key; it cannot read data, write a personal namespace, or impersonate a player. This is a narrow credential with a narrow job.

The result is an abstraction that stays useful across use cases:

- A profile API: `/{username}/profile/main`.
- A game backend: personal saves plus server-owned shared leaderboards.
- A marketplace: personal listings plus an application-managed catalog.
- A content product: personal drafts plus shared releases and configuration.

m-stash is not direct MongoDB access wearing a friendly UI. It is the boundary where identity becomes safe data ownership.

For the API contract, see [integration.md](integration.md). For deployment, see [quickstart.md](quickstart.md).

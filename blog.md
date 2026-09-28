# One identity layer, two data planes

Most applications start with a familiar problem: a frontend needs registration, login, a profile, and somewhere to put user-owned data. Soon it also needs data the application owns: the current leaderboard, a catalog, feature configuration, moderation outcomes, or trusted game state.

A generic database API collapses those concerns into one permission problem. m-stash keeps them distinct.

Open API registration creates a user and an immutable personal namespace atomically. A frontend collects email, password, and username once; the server assigns ownership on every later write. That makes profiles, saves, preferences, submissions, and public portfolios ordinary typed resources without accepting an owner ID from the browser.

Deployment administration is separate. `ADMIN_EMAIL` and `ADMIN_PASSWORD` provision an operator account from the environment. It controls a singleton shared scope but does not need a public username. The shared scope holds application-owned records, with visibility that can be private to admins, readable to signed-in users, or deliberately public.

A trusted scoring service can update a shared leaderboard using `SERVICE_TOKEN`; it cannot read data, write a personal namespace, or impersonate a player. This is a narrow credential with a narrow job.

The result is an abstraction that stays useful across use cases:

- A profile API: `/{username}/profile/main`.
- A game backend: personal saves plus server-owned shared leaderboards.
- A marketplace: personal listings plus an application-managed catalog.
- A content product: personal drafts plus shared releases and configuration.

m-stash is not direct MongoDB access wearing a friendly UI. It is the boundary where identity becomes safe data ownership.

For the API contract, see [integration.md](integration.md). For deployment, see [quickstart.md](quickstart.md).

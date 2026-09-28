# m-stash Workspace

This embedded React workspace demonstrates the m-stash product model:

- Open registration claims a personal username namespace.
- Personal data is owner-scoped and never takes an owner ID from the browser.
- Deployment administrators manage one shared application-data scope.
- Standard users can read shared records intended for signed-in audiences.

Production assets are written to `frontend/dist` and embedded by the Go service, so the UI, API, and browser-session cookies share one origin.

## Development

Start the Go API on port `4000`, then run:

```sh
npm run dev
```

Vite proxies `/v1` requests to `http://127.0.0.1:4000`.

```sh
npm run lint
npm run build
```

The browser never receives MongoDB credentials, a service token, or a namespace ownership value. m-stash derives ownership from the authenticated account for every personal mutation.

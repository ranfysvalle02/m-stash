# m-stash Operator UI

This is the embedded React interface for m-stash. It helps an operator verify the secure MongoDB data path: authenticate, create a private record in the included sample collection, inspect the policy-limited query result, and configure the service.

The UI is not a separate product or deployment. Production builds are written to `frontend/dist` and embedded in the Go binary by [ui.go](../ui.go). The Go service serves the UI, API, and browser-session cookies from one origin.

## Development

Start the Go API from the repository root on port `4000`, then run Vite:

```sh
npm run dev
```

Vite proxies `/v1` requests to `http://127.0.0.1:4000`. Useful commands:

```sh
npm run lint
npm run build
```

Run `make test` from the repository root to build this UI and run the Go tests together.

## Product boundary

The UI can inspect only data allowed by the server's collection rules. It does not receive MongoDB credentials and it does not configure authorization policy from the browser. Configure policies through `M_STASH_CONFIG` and keep credentials in the deployment environment or secret manager.

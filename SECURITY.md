# Security Policy

## Reporting a vulnerability

Please report security issues privately via GitHub's **[Report a vulnerability](https://github.com/ranfysvalle02/m-stash/security/advisories/new)** (Security → Advisories), or email the maintainer. Do not open a public issue for security reports.

Include reproduction steps and impact. You can expect an initial acknowledgement within a few days.

## Scope

m-stash enforces authentication and per-user/per-app data ownership. Reports that undermine those guarantees — cross-tenant or cross-user data access, authentication or session bypass, token or secret-key leakage — are especially important.

## Handling secrets

Never share `MONGO_URI`, `JWT_SECRET`, `PLATFORM_TOKEN`, app secret keys, or admin passwords in a report. Redact them from logs and screenshots.

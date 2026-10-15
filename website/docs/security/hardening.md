---
title: Hardening
description: Login, sessions, CSRF, tokens, input validation, and response headers.
---

# Hardening

This module category can run arbitrary commands on remote servers — boundaries are explicit.

| Concern | Control |
| ------- | ------- |
| Admin login | bcrypt; sessions are random 32-byte tokens stored as SHA-256 hashes in SQLite |
| Session cookie | `cc_session`, `HttpOnly`, `Secure` (configurable), `SameSite=Lax`, 12h TTL |
| CSRF | `requireSameOrigin` on cookie-authenticated mutations; accepts forwarded host + configured public URL; `/api/v1/` and `/webhooks/` exempt (bearer/HMAC) |
| API tokens | Random, stored hashed, scoped `read`/`write`; `write` is admin-equivalent; `last_used_at` updated best-effort |
| Remote safety | IDs/paths validated; POSIX/PS quoting helpers; output capped and redacted before logs/audit |
| Destructive paths | Windows live copies bounded by `deploy.target_root` unless `allow_external_target_paths` |
| Domains | Validated hostnames (no scheme/path/port/whitespace) so they cannot alter the Caddy admin request |
| Repo paths | Repo-relative paths constrained inside the checkout |
| Transfers | File contents base64-encoded to avoid quoting issues; Docker build args redacted |
| API input | Bodies size-limited (64 KiB API, 1 MiB webhooks); unknown JSON fields rejected |
| Headers | `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, `Cache-Control: no-store` |
| Caddy admin | Loopback/management net only; deploys without a domain still run when Caddy is down |

Report suspected credential exposure as an incident: rotate the master key source, re-add affected credentials, revoke API tokens, and review the audit log for the window.

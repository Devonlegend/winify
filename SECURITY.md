# Security Policy

winify connects to remote servers over SSH and WinRM, runs build and deploy
commands on them, encrypts credentials at rest, and stores session and API
tokens. Please treat all of that as security-sensitive and report issues
privately.

## Supported versions

While the project is pre-1.0, only the latest release and the `main` branch
receive security fixes.

| Version | Supported |
| --- | --- |
| `main` and the latest `v*` release | Yes |
| Older releases | No |

## Reporting a vulnerability

**Do not open a public issue for a security problem.** Report it privately using
either channel:

- GitHub Private Vulnerability Reporting — open the **Security** tab of
  [Devonlegend/winify](https://github.com/Devonlegend/winify) and choose
  **Report a vulnerability**.
- Email **<randtequila@gmail.com>**.

Please include, as far as you can:

- what the issue is and the impact you believe it has,
- the version or commit you tested,
- steps to reproduce, ideally a proof of concept,
- any suggested fix or mitigation.

## What to expect

- We aim to acknowledge a report within a few days.
- We will confirm the issue, work on a fix, and coordinate a disclosure
  timeline with you. Credit is offered unless you prefer to stay anonymous.
- Please allow a reasonable window to ship a fix before any public disclosure.

## In scope

- The credential store and its AES-256-GCM encryption (`internal/auth`).
- Remote command execution over SSH and WinRM (`internal/deployment`),
  especially command injection or escaping flaws.
- Webhook HMAC verification, API token scoping, and session handling
  (`internal/server`).
- The reverse proxy and automatic TLS registration (`internal/proxy`).
- The install and upgrade scripts (`packaging/`).

## Out of scope

- Vulnerabilities in third-party software winify integrates with (Caddy,
  Ollama, ChromaDB, SQLite, the Go toolchain). Report those upstream.
- Misconfiguration of your own host, firewall, or DNS.
- Denial of service by an operator who already has legitimate admin access.
- Issues that require an already-compromised admin account.

## Hardening reminders for operators

- Do not expose the listener until first-run registration is complete.
- Keep `deploy.known_hosts_file` set in production;
  `allow_insecure_host_key` is development-only.
- Rotate API tokens and re-issue credentials if a host is compromised. See the
  [security documentation](https://winify.space/security/credentials) for the
  full model.

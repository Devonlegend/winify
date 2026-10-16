<p align="center">
  <img src="internal/static/web/img/logo-dark.svg" alt="DevOps Control Center" width="72" height="72">
</p>

# DevOps Control Center

<p align="center">
  Self-hosted deployments for environments that mix Windows IIS/services and
  Docker — git push to a live HTTPS URL.
</p>

<p align="center">
  <a href="https://github.com/Devonlegend/winify/actions/workflows/ci.yml"><img src="https://github.com/Devonlegend/winify/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://winify.space"><img src="https://img.shields.io/badge/docs-winify.space-2b2b2b" alt="Documentation"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/go-1.27%2B-2b2b2b?logo=go&logoColor=white" alt="Go 1.27+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-2b2b2b" alt="License: MIT"></a>
</p>

A self-hosted deployment platform for environments that mix Windows IIS and
Docker. It owns the deployment lifecycle: it connects directly to target servers
over SSH (Linux/Docker) and WinRM/PowerShell Remoting (Windows/IIS), builds and
deploys on a signed webhook, fronts each deployment with a reverse proxy and
automatic TLS, and polls metrics over the same connections. It is not a
dashboard that watches an external CI system.

- **Docs:** <https://winify.space>
- **Source:** <https://github.com/Devonlegend/winify>

## Features

- **In-dashboard management** of servers, projects and encrypted credentials;
  deploy from the UI or on a signed push webhook.
- **Windows and Docker as first-class targets** — native service (`winsvc`),
  IIS (`iis`), and Docker/Linux (`docker`) share one lifecycle.
- **REST API with scoped tokens** (`/api/v1`) for automation and CI.
- **Webhook-driven deploys** with per-project HMAC verification and deploy
  history.
- **Rollback** to the previous known-good version (Docker image tag or IIS
  timestamped backup).
- **Automatic HTTPS** via Caddy's admin API; Let's Encrypt certificates are
  issued and renewed with no certbot step.
- **Monitoring** of CPU, memory, disk, uptime and service state over the
  existing connections — no agent to install.
- **Local assistant** (ChromaDB + Ollama) grounded in the guides and live
  deploy/monitoring data. No cloud LLM.
- **Encrypted credentials** (AES-256-GCM), never logged or returned by the API.
- **Audit log** of every remote command (target, action, deployment, timestamp).

## Quick install

Windows (elevated PowerShell):

```powershell
irm https://winify.space/install.ps1 | iex
```

Linux (systemd):

```sh
curl -fsSL https://winify.space/install.sh | sh
```

Installers pull the latest GitHub Release asset, verify its SHA-256, and start
winify. Then open `http://localhost:8080` and create the admin account with the
one-time setup token from the log.

## Quick start (from source)

```powershell
go build -o control-center ./cmd/control-center
Copy-Item config.example.yaml config.yaml
.\control-center serve -config config.yaml
```

Open the dashboard at `http://127.0.0.1:8080` and sign in. On first run the
listener is loopback-only, the database and master key are created, and a
one-time setup token is printed in the service log (the Windows installer also
prints it). The token is required on the registration page; do not expose the
listener until registration is complete.

## Documentation

Full documentation lives at **[winify.space](https://winify.space)**.

- [Introduction](https://winify.space/intro)
- [Windows quickstart](https://winify.space/quickstart/windows)
- [Docker quickstart](https://winify.space/quickstart/docker)
- [Concepts and architecture](https://winify.space/concepts/architecture)
- [Configuration reference](https://winify.space/reference/config)
- [Troubleshooting](https://winify.space/troubleshooting/)

The in-app assistant ingests a curated subset of these guides from
`internal/assistant/docs/`.

## Repository layout

```text
cmd/control-center/      entrypoint and admin subcommands
internal/
  config/                YAML/env config and inventory (servers, projects)
  server/                HTTP routes, auth, webhooks, dashboard handlers
  deployment/            SSH/WinRM runners, Docker and IIS pipelines, audit
  monitoring/            metrics collectors and scheduler
  proxy/                 Caddy reverse-proxy registration
  assistant/             ChromaDB + Ollama RAG, curated docs
  auth/                  sessions and encrypted credential store
  models/                SQLite data layer and migrations
  static/                embedded templates, CSS, JS
website/                 Docusaurus documentation site (winify.space)
packaging/               install and upgrade scripts
```

## Testing

```sh
go test ./...
```

Requires Go 1.27+. SQLite is pure-Go (`modernc.org/sqlite`), so no C toolchain
is needed on Windows.

## Credentials and security

Credentials are encrypted at rest and resolved in memory only when a connection
opens. They are never written to logs or returned by any API response. Every
remote command is recorded in the audit log with its target, action and
deployment, but never with credential material.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md) first, and open an issue to discuss
non-trivial changes before opening a pull request.

## License

Released under the [MIT License](LICENSE). Copyright (c) 2026 Devon Legend.

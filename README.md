# DevOps Control Center

A self-hosted deployment platform for environments that mix Windows IIS and
Docker. It owns the deployment lifecycle: it connects directly to target servers
over SSH (Linux/Docker) and WinRM/PowerShell Remoting (Windows/IIS), builds and
deploys on a signed webhook, fronts each deployment with a reverse proxy and
automatic TLS, and polls metrics over the same connections. It is not a
dashboard that watches an external CI system.

## Features

- **In-dashboard management** of servers, projects and encrypted credentials;
  deploy from the UI or on a signed push webhook.
- **REST API with scoped tokens** (`/api/v1`) for automation and CI.
- **Webhook-driven deploys** for Docker and IIS, with per-project HMAC
  verification and deploy history.
- **Rollback** to the previous known-good version (Docker image tag or IIS
  timestamped backup).
- **Automatic HTTPS** via Caddy's admin API; Let's Encrypt certificates are
  issued and renewed with no certbot step.
- **Monitoring** of CPU, memory, disk, uptime and service state over the
  existing connections — no agent to install.
- **Local assistant** (ChromaDB + Ollama) grounded in the guides and live
  deploy/monitoring data.
- **Encrypted credentials** (AES-256-GCM), never logged or returned by the API.
- **Audit log** of every remote command (target, action, deployment, timestamp).

## Quick start

    go build -o control-center ./cmd/control-center
    Copy-Item config.example.yaml config.yaml
    .\control-center serve -config config.yaml

Open the dashboard at `http://127.0.0.1:8080` and sign in. On first run the
listener is loopback-only, the database and master key are created, and a
one-time setup token is printed in the service log (the Windows installer also
prints it). The token is required on the registration page; do not expose the
listener until registration is complete.

## Documentation

- [Setup Guide](internal/assistant/docs/setup-guide.md)
- [Adding a Project or Server](internal/assistant/docs/adding-project-server.md)
- [Troubleshooting](internal/assistant/docs/troubleshooting.md)
- [FAQ](internal/assistant/docs/faq.md)
- [End-to-End Checklist](E2E-CHECKLIST.md)

These guides are also the curated knowledge base the assistant ingests.

## Repository layout

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

## Testing

    go test ./...

Requires Go 1.27+. SQLite is pure-Go (`modernc.org/sqlite`), so no C toolchain
is needed on Windows.

## Credentials and security

Credentials are encrypted at rest and resolved in memory only when a connection
opens. They are never written to logs or returned by any API response. Every
remote command is recorded in the audit log with its target, action and
deployment, but never with credential material. See the Setup Guide for adding
credentials.

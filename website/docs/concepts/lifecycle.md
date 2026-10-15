---
title: Deployment lifecycle
description: Queued to running to success or failed, SSE streaming, in-flight guard, and boot reconciliation.
---

# Deployment lifecycle

```mermaid
sequenceDiagram
  autonumber
  participant GIT as GitHub/GitLab
  participant S as HTTP server
  participant D as Deployer
  participant DB as SQLite
  participant T as Target (SSH/WinRM/local)
  participant C as Caddy
  participant N as Notifier
  GIT->>S: POST /webhooks/github/{id}
  S->>S: verify HMAC, branch, dedupe delivery
  S->>D: Trigger(project, server, commit, ref)
  D->>DB: CreateDeployment(status=queued)
  D-->>S: deployment id
  S-->>GIT: 202 Accepted
  Note over D: async goroutine, bounded by deploy.timeout_minutes
  D->>DB: status=running
  D->>N: notify(pending)
  D->>D: expand project / environment variables
  D->>T: connect + Deploy (clone, build, ship)
  T-->>D: artifact (image tag / backup path)
  D->>DB: status=running, artifact
  D->>C: register domain → upstream
  D->>D: cleanup retired proxy domains
  D->>DB: status=success
  D->>N: notify(success)
```

The browser watches progress over Server-Sent Events (`GET /deployments/{id}/stream`, events `status` / `log`). Status writes and logs use a cancellation-proof context so a timed-out deploy is still finalized in history.

## Deploy states

```mermaid
stateDiagram-v2
  [*] --> queued
  queued --> running: worker starts
  running --> success: target + proxy ok
  running --> failed: error (redacted, persisted)
  success --> [*]
  failed --> [*]
```

- `trigger` is `manual` | `webhook` | `api` | `rollback`.
- Each `Trigger`/`Rollback` writes a row, then spawns a goroutine bounded by `context.WithTimeout(deploy.timeout_minutes)` (default 30). A `sync.WaitGroup` tracks workers; `Stop()` cancels and waits (hard cap 30s) before the database closes.
- An in-flight guard (`sync.Map` keyed by project id) returns `ErrDeployInProgress` for a second concurrent deploy of the same project — surfaced as `409`.
- On boot, sessions are pruned, deployments left `running` by a crash are marked `failed`, and history is pruned by `deploy.retention_days` (default 90). The UI never shows a stale `running`.

## Rollback

Rollback reuses the same pipeline with the previous known-good artifact. It creates a new history row (`trigger=rollback`), so the timeline stays honest. See [Deploy and roll back](../how-to/deploy-rollback.md).

## Variables

Before connecting, the Deployer expands `{{project.KEY}}` / `{{environment.KEY}}` from project env, shared variables, and build env. Secret values are redacted in logs and audit. See [Environment variables](../how-to/env-variables.md).

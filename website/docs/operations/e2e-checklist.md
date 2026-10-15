---
title: End-to-end checklist
description: Take a push to a live HTTPS URL for Docker and IIS, including failure paths.
---

# End-to-end checklist

Follow top to bottom to take a push to a live HTTPS URL for **both** a Docker and an IIS project. PowerShell on the control-center host unless noted.

Placeholders: `CC_HOST` (e.g. `http://127.0.0.1:8090`), `DOCKER_HOST` (e.g. `10.0.0.9`), `WIN_HOST` (e.g. `10.0.0.5`), and the domains.

## Part 0 — Prerequisites

- [ ] Docker target: reachable over SSH; `git`, `docker`, `docker compose` plugin installed.
- [ ] `deploy.known_hosts_file` populated from a trusted source (fail-closed).
- [ ] IIS target: WinRM enabled (`winrm quickconfig`), account able to stop services / recycle pools; `git` + build tooling.
- [ ] Repo with a Dockerfile (Docker) or publishable web app (IIS).
- [ ] DNS name per app pointing at the Caddy host; ports 80/443 reachable for Let's Encrypt.

## Part 1 — Control center setup

- [ ] Build and configure:

  ```powershell
  go build -o control-center ./cmd/control-center
  Copy-Item config.example.yaml config.yaml
  ```

- [ ] Set `proxy.enabled: true`, `proxy.admin_url`, and a free listen address.
- [ ] Start Caddy with admin reachable:

  ```bash
  docker run -d --name caddy --network host caddy:2 caddy run --admin 127.0.0.1:2019
  ```

- [ ] Add encrypted credentials (`server-002-ssh`, `server-001-winrm`, webhook secrets).
- [ ] Edit `servers.yaml` / `projects.yaml` (fresh install only) or use **Servers → Onboard a Windows server**.
- [ ] Start and sign in (setup token from startup log; keep loopback until admin exists):

  ```powershell
  .\control-center serve -config config.yaml
  ```

## Part 2 — Docker (push → build → deploy → HTTPS)

- [ ] Push a commit to the branch.
- [ ] Simulate the webhook, or push for real. Expect `202 {"status":"accepted","deployment_id":N}`. Resending the same `X-GitHub-Delivery` returns `202 reason=duplicate delivery` with no second deployment.
- [ ] Deployment tab: `queued → running → success` with clone, `docker build`, `compose up -d`, health check, proxy registration.
- [ ] On the Docker host: `docker ps` shows the container.
- [ ] Open `https://<docker-domain>` — valid certificate.
- [ ] `GET /api/v1/audit` shows clone/build/compose with `action=deploy` and the deployment id.

## Part 3 — IIS (push → validate → backup → deploy → HTTPS)

- [ ] Same webhook flow for the IIS project/secret.
- [ ] Log shows: sync, build, validate `web.config`, stop service/pool, backup, copy, start, recycle pool, smoke test, proxy registration.
- [ ] Open `https://<iis-domain>`.
- [ ] Timestamped backup exists under `iis_backup_dir`.

## Part 4 — Failure paths (must be clear, never silent)

- [ ] Bad signature → `401`, no deployment.
- [ ] Wrong branch → `202 reason=branch mismatch`, no deploy.
- [ ] Unreachable server → Monitoring card red with `down` pill + error within `monitoring.timeout_seconds`; no stale numbers.
- [ ] Failed deploy (bad Dockerfile path) → `failed` + error banner + full log; previous version keeps serving.
- [ ] Ollama down → assistant still returns a grounded answer from docs/live data.

## Part 5 — Rollback

- [ ] Deploy good, then bad. Click **Roll back**.
- [ ] New history entry `trigger=rollback`; Docker reuses previous tag (no rebuild), IIS restores latest backup.
- [ ] Previous version live again.

## Part 6 — Cleanup

- [ ] Stop winify (Ctrl-C); remove the Caddy container if unneeded.

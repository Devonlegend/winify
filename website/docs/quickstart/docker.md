---
title: Docker/Linux quickstart
description: Connect a Linux/Docker host over SSH and ship a container to HTTPS.
---

# Docker/Linux quickstart

Use this when your workload runs on a Linux host with Docker. winify reaches it over SSH; no agent is installed.

## Quick install

On the Linux controller host (systemd, amd64/arm64):

```bash
curl -fsSL https://winify.space/install.sh | sh
```

Options: `curl -fsSL ... | sh -s -- --port 8080 --version v0.1.0`.
The script resolves the latest GitHub Release, verifies SHA-256, installs to
`/usr/local/bin/winify`, and enables the `winify` systemd unit.

## Prerequisites

- Linux host reachable over SSH with `git`, `docker`, and the `docker compose` plugin.
- A trusted `known_hosts` file — SSH verification is **fail-closed**.
- A repo with a `Dockerfile` (or compose file, or prebuilt image).
- A DNS name pointing at the Caddy host, with ports 80/443 reachable for Let's Encrypt.
- Local: Go 1.27+ to build, or a release binary.

## 1. Build and configure

```bash
go build -o control-center ./cmd/control-center
cp config.example.yaml config.yaml
```

In `config.yaml`:

```yaml
server:
  addr: "127.0.0.1:8080"
proxy:
  enabled: true
  admin_url: "http://127.0.0.1:2019"
deploy:
  known_hosts_file: "/etc/winify/known_hosts"  # populate from a trusted source
```

Start Caddy with its admin API reachable from winify. The admin API is unauthenticated — keep it on loopback or a protected management network. Never publish port 2019 publicly:

```bash
docker run -d --name caddy --network host caddy:2 caddy run --admin 127.0.0.1:2019
```

## 2. Add credentials

Secrets live encrypted in SQLite, referenced as `vault:<name>`:

```bash
./control-center cred add server-002-ssh < ~/.ssh/id_ed25519
./control-center cred add proj-001-webhook
```

Paste or pipe the value when prompted. Values are never logged or returned by the API.

## 3. Add server and project

`servers.yaml` / `projects.yaml` seed the database **only on a fresh install**. After that the database is authoritative — manage everything in the UI.

Minimal Docker server:

```yaml
servers:
  - id: server-002
    name: docker-01
    type: docker
    ssh_host: 10.0.0.9
    ssh_port: 22
    ssh_user: deploy
    ssh_key_ref: "vault:server-002-ssh"
```

Minimal Dockerfile project:

```yaml
projects:
  - id: proj-001
    name: my-app
    server_id: server-002
    source: dockerfile
    repo_url: https://github.com/you/my-app
    branch: main
    dockerfile_path: Dockerfile
    domain: myapp.example.com
    ports_exposes: 8080
    health_path: /healthz
    webhook_secret_ref: "vault:proj-001-webhook"
```

Start and sign in (first run needs the setup token from the startup log):

```bash
./control-center serve -config config.yaml
```

## 4. Push to deploy

Push to `main`. Or simulate the GitHub webhook:

```powershell
$secret = "webhook-secret-001"
$body   = '{"ref":"refs/heads/main","after":"abc1234def"}'
$h = New-Object System.Security.Cryptography.HMACSHA256
$h.Key = [Text.Encoding]::UTF8.GetBytes($secret)
$sig = "sha256=" + (($h.ComputeHash([Text.Encoding]::UTF8.GetBytes($body)) | ForEach-Object { $_.ToString("x2") }) -join "")
[IO.File]::WriteAllText("$env:TEMP\push.json", $body)
curl.exe -i -X POST -H "X-GitHub-Event: push" -H "X-Hub-Signature-256: $sig" --data-binary "@$env:TEMP\push.json" http://127.0.0.1:8080/webhooks/github/proj-001
```

Expect `202 {"status":"accepted","deployment_id":N}`. The Deployment tab shows `queued → running → success` with clone, `docker build`, `compose up`, health check, and proxy registration. Open `https://myapp.example.com`.

## Next

- [First deploy](./first-deploy.md)
- [Webhook wiring](../how-to/webhooks.md)
- [Deploy sources and rollback](../how-to/deploy-rollback.md)

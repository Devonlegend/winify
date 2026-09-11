# Local Docker End-to-End Test

This directory spins up a throwaway Linux "target server" on your machine so you
can run the full Docker deploy pipeline without a real VM:

- `target/` — a container running a real Docker daemon (privileged) plus `sshd`,
  `git` and the Docker Compose plugin.
- `sample-app/` — a tiny Python HTTP app with a Dockerfile and a `/healthz`
  endpoint, committed to a git repo inside the target.
- `config.yaml` — a self-contained control-center config using `dev/data/` for
  its database, so your real data is untouched.
- `setup.ps1` — builds the target, creates the sample repo, installs an SSH key
  and stores the credentials.
- `deploy.ps1` — sends a signed push webhook to trigger a deploy.

## Run it

```powershell
# 1. Make sure Docker Desktop is running.
docker info

# 2. One-time setup (also re-runnable to reset the target).
powershell -ExecutionPolicy Bypass -File dev\setup.ps1

# 3. Start the control-center (uses the dev config).
go run ./cmd/control-center -config dev/config.yaml

# 4. In another terminal, trigger a deploy.
.\dev\deploy.ps1
```

Open http://localhost:8090 (`admin` / `admin`) and watch the **Deployment** tab:
the attempt goes queued → running → success, then the app is live at
http://localhost:18080.

## What this exercises

- Signed webhook (HMAC) → pipeline selection → SSH connection → `git clone` →
  `docker build` → `docker compose up -d` → HTTP health check.
- Deploy history with logs, the audit log (`GET /api/audit`), and monitoring of
  the target.
- Rollback: after two successful deploys, click **Roll back** on the Deployment
  tab (Docker rollback reuses the previous image tag).

## Not covered locally

The live HTTPS URL needs a real domain pointing at a running Caddy with ports
80/443 reachable. Set `proxy.enabled: true` and follow `E2E-CHECKLIST.md` Part 1
to test TLS. For IIS, use a Windows VM with WinRM enabled.

## Teardown

```powershell
docker rm -f cc-target
Remove-Item -Recurse -Force dev\data, dev\keys, dev\control-center.exe -ErrorAction SilentlyContinue
```

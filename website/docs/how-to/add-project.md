---
title: Add a project
description: Docker, Windows service, and IIS projects, deploy sources, and repo auto-detection.
---

# Add a project

Projects are deployable units bound to one server. Push to `branch` triggers a deploy; `webhook_secret_ref` names the encrypted secret verifying the webhook; `domain` is registered with Caddy on success.

Use **Projects → New** (guided wizard with smart defaults) or `POST /api/v1/projects`.

## Common fields

| Field | Notes |
| ----- | ----- |
| `id`, `name` | `id` validated (no separators / `..`) |
| `server_id` | Must reference an existing server |
| `branch` | Push branch; other branches are ignored (`202 branch mismatch`) |
| `domain` | Validated hostname (no scheme/path/port); registered in Caddy |
| `port` / `ports_exposes` / `ports_mappings` | `ports_exposes` is in-container port; `ports_mappings` are `host:container`; proxy dials `EffectiveHostPort()` |
| `health_path` | Must return 2xx/3xx, e.g. `/healthz` |
| `disable_health_check` | `true` for workers/cron with no HTTP endpoint |
| `webhook_secret_ref` | `vault:<name>` of the HMAC secret |
| `env`, `build_env` | Runtime env vs build-time args; write-only in UI/API (`[redacted]`) |
| `project_group`, `environment` | Coolify-style grouping; default when empty |
| `github_repo` | `owner/repo` for GitHub App auto-registration |

## Docker project

Pick the Docker server, then the **deploy source**:

- **dockerfile** — build from repo. `repo_url`, `branch`, `dockerfile_path` (default `Dockerfile`).
- **compose** — deploy the repo's own compose file. `repo_url`, `branch`, `compose_path` (default `docker-compose.yml`). Project `env` is written to `.env` next to the compose file.
- **image** — run a prebuilt registry image. `image` (e.g. `nginx:1.27`), optional `ports_exposes`/`ports_mappings`; `repo_url` not needed.

Health timing overrides: `health_interval_seconds`, `health_timeout_seconds`, `health_retries`, `health_start_period_seconds` (fall back to global `deploy.*`).

## Windows native-service project

Pick the `winsvc` server. Fill in:

- `service_name`, `service_exe`, `service_work_dir` (required except static sites)
- `service_build_command`, `service_source_subdir` (default `.`), `service_args`, `service_log_dir`, `service_account`
- `runtime`: `python` | `node` | `go` | `dotnet` (empty = none provisioned; winify installs a missing runtime)
- `caddy_mode`: `none` | `proxy` | `static` — `static` needs no service name/exe; per-target Caddy serves files

Pipeline validates before touching live, backs up, copies, installs/updates NSSM service, starts, smoke-tests, and compensates (restore + restart) on failure.

## IIS project (advanced/legacy)

Pick the IIS server. Fill in `iis_site` (created if missing), `iis_physical_path`, `iis_app_pool` (created if missing), plus optional `iis_service`, `iis_build_command`, `iis_source_subdir`.

Enable **Blue-green** (`iis_blue_green: true`) for near-zero downtime: build goes to the inactive slot (`<path>.blue`), is validated there, then the site is repointed without stopping the pool. Rollback repoints — no copy.

## Repo auto-detection

**Projects → New → Detect** (or `POST /projects/detect`) inspects the repo and proposes a build/run plan with confidence and evidence:

| Stack | Evidence | Proposal |
| ----- | -------- | -------- |
| Go | `go.mod` | `go build -o app.exe`, reads `go 1.x` |
| .NET | `.csproj`/`.sln` | `dotnet publish -c Release -o out`; `web.config` → IIS else service |
| Python | `requirements.txt`/`pyproject.toml`/entry | venv + pip; Django/FastAPI/Flask ports |
| Node | `package.json` | `npm ci` (+ build); Next/Vite/Express |
| Static | `index.html` | Per-target Caddy static on 8080 |

## Credentials

**Credentials** tab or CLI:

```bash
./control-center cred add server-002-ssh
./control-center cred add proj-001-webhook
```

Reference by name (`ssh_key_ref` / `credential_ref` / `webhook_secret_ref`). Values are AES-256-GCM encrypted, decrypted only in memory at connection time.

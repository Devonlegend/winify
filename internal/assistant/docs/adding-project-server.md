<!-- GENERATED from website/docs — do not edit by hand. Edit the source and run scripts/sync-assistant-docs.sh. -->

# Adding a Project or Server



The database is the source of truth; `servers.yaml` is imported only once, when the tables are empty (fresh install). After that, manage everything in the UI (**Servers** tab) or the [REST API](../reference/api.md).

## Fastest path: onboard a Windows server

**Servers → Onboard a Windows server**: give the WinRM endpoint, user, and password. winify provisions directories, NSSM, and Caddy on the target over WinRM and creates the server record for you.

The remote must already accept WinRM. One-time snippet on the remote (elevated):

```powershell
Enable-PSRemoting -Force
```

## Manual entry

Choose the type — it selects the deploy pipeline:

- **winsvc — Windows native service (recommended)**. Default for Windows: app runs as a Windows service via NSSM, Caddy provides HTTPS/static. Set `winrm_endpoint`, `winrm_user`, `winrm_insecure`, and the WinRM credential ref; NSSM/Caddy paths default to `C:\ProgramData\winify\tools`.
- **docker — Linux/Docker over SSH**. Set `ssh_host`, `ssh_port` (default 22), `ssh_user`, and the SSH key credential ref (e.g. `vault:server-002-ssh`).
- **iis — IIS (advanced/legacy)**. Only for ASP.NET Framework, Windows auth, classic ASP, or an existing IIS site.

Field reference (YAML keys match API `snake_case`):

| Field | Used by | Notes |
| ----- | ------- | ----- |
| `id`, `name` | all | `id` must not contain path separators or `..` |
| `type` | all | `winsvc` \| `iis` \| `docker` |
| `ssh_host`, `ssh_port`, `ssh_user`, `ssh_key_ref` | `docker` | Key is a `vault:` ref to a PEM/OpenSSH private key |
| `winrm_endpoint`, `winrm_user`, `winrm_transport`, `winrm_insecure`, `credential_ref` | `winsvc`, `iis` (remote) | `ntlm` default; `basic` requires HTTPS; HTTP requires `winrm_insecure: true` |
| `local` | `winsvc` | `true` for the host winify runs on — in-process PowerShell, no credential |
| `host` | all | Proxy upstream override; falls back to `ssh_host`, then WinRM endpoint host |
| `nssm_path`, `caddy_path` | `winsvc` | Permanent tool paths; must not change once services are installed |
| `public_ip` | all | Generates `<name>.<ip>.sslip.io` when you have no DNS yet |
| `services` | all | Service names for Monitoring (systemd units / Windows service names) |
| `disk_path` | all | Volume to report (`/` Linux, `C:` Windows) |

## SSH trust (Docker)

Populate `deploy.known_hosts_file` from a trusted provisioning source before registering Docker targets. Verification is fail-closed. Only the explicit `deploy.allow_insecure_host_key: true` bypasses it — **never in production**.

## WinRM trust

- `winrm_transport: ntlm` (default) or `basic` (`basic` requires HTTPS).
- HTTP endpoints require explicit `winrm_insecure: true`.
- For self-signed HTTPS certificates set the same flag; prefer a trusted certificate in production.
- The WinRM account can install services and write the filesystem — treat it as highly privileged.

## Verify

After saving, open **Monitoring**. Within `monitoring.interval_seconds` the server reports CPU/memory/disk, or a red `down` pill with the exact connection error. If deploys work but monitoring is down, read the error text on the card — same connection, different command.



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
| `git_credential_ref` | `vault:<name>` for private repos (see below) |
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

## Private repositories

Set a project's `git_credential_ref` to a credential holding either:

- an **SSH private key** (a GitHub/GitLab deploy key) — use it with an SSH repo URL (`git@github.com:org/app.git`); winify uploads the key to the target with owner-only permissions and points git at it, or
- an **HTTPS access token** (a PAT) — use it with an HTTPS repo URL; the token is passed as an `http.extraHeader`, never embedded in the URL or logged.

Either way the secret never appears in `repo_url`, deploy logs, or the audit log, and repository detection uses it too (pass `git_credential_ref` alongside `repo_url` in the Detect step).

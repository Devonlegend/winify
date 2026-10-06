# winify — Complete Walkthrough & Q&A Bank

> Everything you need to present winify: the problem, the design, every component,
> the live demo, and an answer for (almost) every question a lecturer or student
> can ask. Written from the actual code in this repository.

---

## 0. The 30-second pitch

> **winify is a self-hosted DevOps Control Center that owns the whole deployment
> lifecycle — for environments that mix Windows/IIS and Docker. You point it at a
> git repo; it builds, deploys to your server, fronts it with an automatic HTTPS
> URL, monitors it, and keeps a command-level audit trail. It runs as one Go
> binary on your own machine, with a local AI assistant — no cloud, no agent, no
> external CI.**

Three things make it different from Coolify/Portainer/Render:
1. **Windows/IIS and native Windows services are first-class**, not an afterthought next to Docker-only tooling.
2. **It owns the loop end-to-end** (connect → build → deploy → proxy → TLS → monitor → audit). GitHub Actions is at most an optional trigger, never the engine.
3. **It survives hostile networks.** When the machine is behind CGNAT and cannot accept inbound connections, winify provisions a **Cloudflare Tunnel** automatically so you still get a public HTTPS URL.

---

## 1. Problem statement

### 1.1 The real-world situation
You have a machine (a Windows PC or server). You have applications:
- a **native Windows service** (a `.exe` managed by NSSM),
- an **IIS website** (ASP.NET, or a static site),
- **Docker containers** (on the same box, or on a Linux host you reach over SSH).

You want the thing Coolify/Render/Vercel give Linux/Docker people:
`git push` → build → deploy → live HTTPS URL → see what happened → roll back in one click.

But the existing tools assume:
- the target is Linux + Docker,
- you have a normal public IP with ports 80/443 open,
- you have a cloud account, a CI runner, and a domain with a certificate pipeline.

In the real environment (mixed Windows + Docker, behind an ISP that uses **CGNAT**, self-hosted, sometimes offline), none of that holds.

### 1.2 The pain, concretely
- **No Windows story.** IIS and Windows services are handled with manual PowerShell scripts, robocopy, and tribal knowledge.
- **No inbound path.** Behind CGNAT the "public IP" is the carrier's NAT address; ports 80/443 never reach you. sslip.io and "just open a port" are dead ends.
- **No memory.** When something breaks there is no reliable "what command ran, on which server, for which deploy."
- **Secrets on disk.** Connection passwords and webhook secrets end up in scripts or `.env` files.
- **CI is the wrong center of gravity.** GitHub Actions builds on their runners but still can't deploy to your Windows box or manage IIS app pools; you end up gluing a dashboard onto an external CI anyway.

### 1.3 The one-sentence thesis
> The deployment lifecycle should be owned by a single self-hosted control plane
> that treats **Windows/IIS and Docker as equals**, works **behind CGNAT**, and
> keeps an **auditable record of every remote command** — with **no agent to
> install** on the targets.

---

## 2. What winify is (positioning)

- **Self-hosted**: one Go binary + SQLite. No Kubernetes, no message queue, no external DB required.
- **Lifecycle owner**: it connects to targets, builds, deploys, registers the reverse-proxy route, issues TLS, monitors, and records audit.
- **Windows-first, Docker-equal**: `winsvc` (native service), `iis`, and `docker` are all first-class targets.
- **Agentless**: targets only need SSH (Linux/Docker) or WinRM/PowerShell Remoting (Windows). No software installed on the target beyond the workload itself.
- **Offline-capable AI**: a local RAG assistant (ChromaDB + Ollama) grounded in the platform's own docs and live data. Never a cloud LLM.
- **Dependency-light**: standard library + a handful of well-known Go modules.

---

## 3. Who it's for

A **single self-hosting operator** — technical, comfortable on the command line,
learning Go — who runs a mix of Windows and Linux/Docker workloads and wants one
place to deploy and monitor them. They arrive **mid-task and impatient**: ship an
app, check whether a deploy succeeded, add a server. Not to browse.

Secondary role: the same person as **admin** — credentials, API tokens, audit.

V1 is explicitly **single admin, no multi-tenant / RBAC**.

---

## 4. The product principles (and why they matter)

1. **Guided over raw.** A wizard with smart defaults beats a wall of fields. Advanced options stay out of the way until needed.
2. **The operator is mid-task.** Fast, scannable, consistent. Status is always visible; failures explain themselves.
3. **Honest state.** Never show stale or guessed data. Down is down; pending is pending; errors are surfaced, not swallowed.
4. **Server-rendered, dependency-light.** No JS framework, no build step. Enhancement is progressive and degrades to plain HTML.
5. **One lifecycle, both target types.** Windows and Docker share history, audit, monitoring and proxy. Differences are documented, not hidden.

These come from real design review (see `DESIGN.md` and `.impeccable/`), not marketing.

---

## 5. Architecture at a glance

### 5.1 Stack
| Concern | Choice | Why |
|---|---|---|
| Language | **Go 1.27** | Single static binary, great concurrency for long deploys/streams, easy cross-compile |
| HTTP | **net/http + chi** | stdlib-first; chi adds small routing niceties |
| DB | **SQLite via `modernc.org/sqlite`** | Pure Go — no C toolchain on Windows |
| SSH | **`golang.org/x/crypto/ssh`** | Docker/Linux targets |
| WinRM | **`github.com/masterzen/winrm`** | Windows/IIS/service targets, NTLM |
| Reverse proxy / TLS | **Caddy admin API** | Automatic HTTPS, one JSON API |
| UI | Embedded `html/template` + vanilla CSS/JS | No build step, works offline |
| AI | **ChromaDB + Ollama** (in-memory fallback) | Local/offline RAG |
| Config | `gopkg.in/yaml.v3` + `CC_*` env | File + env overrides |
| Service | **`golang.org/x/sys/windows/svc`** | winify runs itself as a native Windows service |

### 5.2 Repository layout
```
cmd/control-center/          entrypoint + admin subcommands (serve, bootstrap, hash-password, cred)
internal/
  config/                    YAML/env config, inventory entities, validation
  server/                    HTTP routes, auth middleware, webhooks, dashboard handlers, REST API
  deployment/                SSH/WinRM runners, Docker/IIS/winsvc pipelines, audit, targets
  monitoring/                agentless metrics collector + scheduler
  proxy/                     Caddy reverse-proxy registration (+ ACME skip)
  tunnel/                    Cloudflare Tunnel provisioning for CGNAT hosts
  bootstrap/                 zero-touch host provisioning steps
  assistant/                 ChromaDB + Ollama RAG + curated docs
  auth/                      sessions, encrypted credential store, API tokens
  models/                    SQLite data layer + migrations
  detect/                    repo inspection → build/run plan
  githubapp/                 GitHub App JWT, installation tokens, webhooks
  notify/                    deployment notifications (webhook, Slack, GitHub)
  service/                   native Windows service wrapper + elevation
  static/                    embedded templates, CSS, JS
projects.yaml, servers.yaml   seed inventory on a fresh install
```

### 5.3 Data flow (one line each)
- **Deploy**: webhook/UI/API → `Deployer.Trigger` → async goroutine → `Target.Deploy` (SSH/WinRM/local) → health check → proxy register → status written to DB.
- **Monitor**: scheduler → `Collector.Collect` → SSH/WinRM command → parse `key=value` → persist sample.
- **Assistant**: question → intent + entity detection → retrieve docs + live data → Ollama generate (or deterministic fallback) → answer + sources.
- **Proxy**: deploy success → `Caddy.Register(host, upstream)` via admin API.

---

## 6. The lifecycle (the narrative you tell)

```
        git push / webhook            UI "Deploy"            REST API
                 \                       |                     /
                  \                      v                    /
                   +--------->  Deployer.Trigger  <---------+
                                      |
             queued deployment row + in-flight lock (one per project)
                                      |
                     +----------------+----------------+
                     |          Target.Deploy           |
                     |  (winsvc / iis / docker)         |
                     +----------------------------------+
                      connect → validate → backup →
                      build/deploy → start → smoke test
                                      |
                         health check (retries, startPeriod)
                                      |
                    success → Caddy.Register(domain→upstream)
                                      |
                     deployment row finalized + notifier fires
                                      |
                    monitoring keeps sampling over the same connection
```

Every step is written to the deployment log (streamed to the UI over SSE) and every
remote command is written to the **audit log**.

---

## 7. Target types (deep dive)

winify has three target types, all sharing one lifecycle.

### 7.1 `winsvc` — native Windows service (the default, recommended)
A built executable run as a Windows service, managed by **NSSM**.

**Pipeline (in order):**
1. `git clone/fetch` + checkout the requested revision (branch resolved via `origin/*`; `git clean -fdx`; `-c safe.directory` for LocalSystem vs user-owned clones).
2. **Ensure the build runtime** (`python` / `node` / `go` / `dotnet`) — installs it if missing (winget/choco), so the build self-heals.
3. **Optional build** (`service_build_command`, e.g. `go build`, `dotnet publish`, `pip install -r requirements.txt`). Build env is redacted from audit.
4. **Ensure NSSM** on the target (upload from `deploy.nssm_source` in chunked base64 if missing; SHA-256 verified).
5. **Validate before touching anything live**: NSSM present, source dir exists, the service exe exists inside the build output.
6. **Stop the service** (a no-op on first deploy).
7. **Timestamped backup** of the install directory (`NO_BACKUP` if it's the first deploy).
8. **Deploy files** (robocopy `/MIR`).
9. **Install/update the service** (idempotent `nssm install`/`nssm set Application/AppDirectory/AppParameters/AppEnvironmentExtra/AppExit/Start`).
10. **Set service environment** (`AppEnvironmentExtra`) — reset to empty if no env, so removed secrets don't survive.
11. **Start and wait for `Running`** (30s deadline).
12. **Optional per-target Caddy** (static file serving; central Caddy still owns public TLS).
13. **HTTP smoke test**.
14. **Compensating recovery** on any failure: restore the backup + restart the service, then mark the deploy `failed`.

**Rollback**: restore the newest timestamped backup on the target and restart. Discovered on the target, so it doesn't depend on history.

### 7.2 `iis` — IIS application (advanced/legacy)
Same shape, plus IIS specifics. **Zero-touch IIS**: winify creates the app pool
and site if missing and points an existing site at the project's path and pool.

**Pipeline:** clone → runtime → optional build → **ensure IIS site/app pool** →
validate (`web.config` XML + app pool + optional service) → choose normal or
blue-green → stop service+pool → backup → robocopy files → start+recycle pool →
smoke test.

**Blue-green (slot swap)** — the differentiator:
- The live directory is **never overwritten**. The build is copied to the **inactive slot** (`<path>` / `<path>.blue`), validated there, then the site's `physicalPath` is repointed atomically and the app pool is recycled.
- A marker file (`<path>.active`) records which slot is live.
- **Rollback = repoint to the other slot** — no file copy, effectively instant.
- On a failed swap/smoke test it swaps back.

**Rollback (non-blue-green)**: restore the newest timestamped backup.

### 7.3 `docker` — Linux target over SSH
Three sources:

| `source` | What it does |
|---|---|
| `dockerfile` (default) | clone repo → `docker build -t cc/<project>:<sha> -f <Dockerfile> --build-arg ...` → write a generated compose file → `docker compose up -d --remove-orphans` → health check |
| `compose` | clone repo → write `.env` from project env → `docker compose -f <compose> up -d --build --remove-orphans` → health check |
| `image` | run a prebuilt registry image via a generated compose file → health check |

**Port model**: `ports_exposes` is the port inside the container; optional
`ports_mappings` publish `host:container`. If no mapping, the exposed port is
published 1:1 so the central proxy can reach it. The proxy dials `EffectiveHostPort()`.

**Rollback**:
- `dockerfile`/`image`: re-run the previous image tag — **no rebuild**.
- `compose`: check out the previous commit and re-run its compose file.

**On deploy failure**: winify attempts to **restore the previous successful release** automatically (if one exists).

### 7.4 Health checks (shared)
- HTTP GET to `health_path` on `EffectiveHostPort()` of the target.
- Timing from the project (`health_interval_seconds`, `health_timeout_seconds`, `health_retries`, `health_start_period_seconds`) falling back to global `deploy.*`.
- `disable_health_check: true` for workers/cron (no HTTP endpoint).
- On Docker failure it dumps `compose ps` + `compose logs` into the deploy log so the cause is visible.

---

## 8. Repo auto-detection (`internal/detect`)

Inspect a repo and propose a build/run plan, Render/Railway style. Detected stacks:

| Stack | Detection | Proposed plan |
|---|---|---|
| **Go** | `go.mod` | `go build -o app.exe [./cmd/x]`, exe `app.exe`; reads the `go 1.x` version |
| **.NET** | `.csproj` / `.sln` | `dotnet publish -c Release -o out`; if `web.config` present → **IIS** target, else service with `dotnet out\App.dll`; reads `<TargetFramework>` / `<AssemblyName>` |
| **Python** | `requirements.txt` / `pyproject.toml` / `Pipfile` / `setup.py` / entry file | venv + pip install; framework: **Django** (`manage.py runserver :8000`), **FastAPI** (`uvicorn <module>:app :8000`), **Flask** (`flask run :5000`), else generic |
| **Node** | `package.json` | `npm ci` (+ `npm run build`); **Next.js** (`next start -p 3000`), **Vite** (static `dist`, no process), **Express**/generic entry |
| **Static** | `index.html` | per-target Caddy static, port 8080 |

Each plan carries **confidence** (high/medium/low) and **evidence** (which files
justified it). This is the approved V1 exception to the "no auto-detection in V2"
rule.

---

## 9. Credentials & security

### 9.1 Credential store (`internal/auth/credentials.go`)
- **AES-256-GCM**. Values are encrypted at rest in SQLite (`credentials` table: nonce + ciphertext).
- The **master key**: from `CC_MASTER_KEY` (base64 32 bytes), `credentials.master_key`, or generated into `<data dir>/master.key` on first use.
- Credentials are referenced by **`vault:<name>`** and resolved **in memory only at connection time**.
- **Never** logged and **never** returned by any API (API redacts env and repo URLs).
- Env values and binary payloads are marked sensitive so the **audit log records a label, not the payload**.

### 9.2 SSH security
- Host-key verification is **fail-closed**: `deploy.known_hosts_file` must be populated. An emergency `allow_insecure_host_key` exists but is documented as dev-only.

### 9.3 WinRM security
- `winrm_transport`: `ntlm` (default) or `basic` (basic requires HTTPS).
- HTTP endpoints require explicit `winrm_insecure: true`.
- The WinRM account can install/manage services and write the filesystem — treated as highly privileged and documented as such.

### 9.4 Web app security
- bcrypt admin password; sessions are random 32-byte tokens stored only as SHA-256 hashes.
- Session cookie: `HttpOnly`, `Secure` (configurable), `SameSite=Lax`, 12h TTL.
- State-changing requests pass a **same-origin** check (accepts the proxy-forwarded host).
- The UI/CSS/JS are embedded; assets are content-fingerprinted so a rebuilt binary busts the browser cache.

### 9.5 Input hardening (defense-in-depth)
- Resource IDs validated (no path separators / `..`).
- Domains validated as hostnames (no scheme/path/port/whitespace) so they can't alter the Caddy admin request.
- Repo-relative paths constrained inside the checkout.
- Target paths constrained under `deploy.target_root` (unless explicitly opted out).
- PowerShell values single-quoted and escaped; file contents transferred **base64** to avoid quoting issues; Docker build args redacted.
- API JSON bodies are size-limited (64 KiB) and reject unknown fields.

---

## 10. Reverse proxy & TLS (`internal/proxy`, Caddy)

- **Caddy** is installed by bootstrap and exposes an **admin API** on `127.0.0.1:2019`.
- On a successful deploy, winify **upserts a route** with a deterministic `@id` (`cc-route-<sha256(host)[:24]>`), so redeploying replaces rather than duplicates.
- Route = host matcher → `reverse_proxy` to the target upstream.
- **Automatic HTTPS**: Caddy obtains/renews Let's Encrypt certificates for real domains.
- **ACME skip** (`proxy.acme_skip`, default `["*.sslip.io"]`): hosts that can't pass ACME are served over plain HTTP behind an external terminator. This is what makes **sslip.io** and **tunnels** work without redirect loops.
- **DNS sanity warning**: after registering, winify resolves the domain and warns if it doesn't point at the proxy host.
- **Retired domains** are tracked (`retired_proxy_domains`) and cleaned up after a domain change, so stale routes don't linger.
- If Caddy is unreachable at startup, winify warns (deploys with a domain will fail) instead of failing every deploy later.

---

## 11. Tunnel (`internal/tunnel`) — the CGNAT solution

### 11.1 Why it exists
Behind CGNAT/private ISP, the machine cannot accept inbound connections. Public-IP
schemes (sslip.io) cannot work. A **tunnel makes an outbound connection** and gives
you a public HTTPS URL. This is what turned "install on a fresh PC" into "and it
gets a public URL".

### 11.2 What it does (Cloudflare)
Given an API token + domain, winify:
1. Resolves the **zone** for the domain.
2. Finds or creates the **named tunnel**.
3. Fetches the **connector token**.
4. Sets **ingress** `*.domain → http://localhost:80` (Caddy), with a 404 catch-all.
5. Creates the **proxied wildcard CNAME** `*.domain → <tunnel-id>.cfargotunnel.com`.
6. Installs **cloudflared** as an NSSM service running `tunnel run --token-file <file>`.
7. Auto-adds `*.domain` to Caddy's **ACME skip**.

Everything is **idempotent**: re-running reuses the tunnel, ingress and DNS record.
Because a wildcard ingress + wildcard DNS are created **once at setup**, per-deploy
winify never calls the provider again — it just registers the host in Caddy.

### 11.3 Config
```yaml
tunnel:
  enabled: true
  provider: cloudflare
  domain: example.com
  account_id: <cloudflare account id>
  tunnel_name: winify
  api_token_ref: "vault:cloudflare-api"   # encrypted
  origin: "http://localhost:80"
  connector_source: ""                    # local cloudflared.exe (wins)
  connector_url: ""                       # or download URL
  service_name: winify-tunnel
```

### 11.4 Security
The API token can create tunnels and DNS records for its account/zone — stored
encrypted, least-privilege scopes, never logged. The **connector token** is a
bearer credential for the tunnel, written only to a protected token file.

### 11.5 What's simplified today
- **Cloudflare only** (the provider is isolated behind a package; ngrok/Tailscale not implemented).
- **Local host only** (not over remote WinRM bootstrap).
- The control-center UI is **not** auto-routed by the tunnel step; exposing the admin panel is a deliberate operator choice.

---

## 12. Bootstrap — zero-touch host provisioning (`internal/bootstrap`)

An idempotent step engine. Each step has `Check` (already satisfied?) and `Apply`
(do it), and the result is recorded in `app_meta` so re-runs skip done steps and
repair failed ones. Must run **elevated** (UAC, or the winify service as
LocalSystem).

| Step | What it does |
|---|---|
| `dirs` | Create `%ProgramData%\winify\{tools,work,apps,backups,logs,data}` |
| `nssm` | Ensure `tools\nssm.exe` (upload from source or download + SHA-256 pin) |
| `winrm` | `Enable-PSRemoting -Force` (only if enabled; refuses public profiles) |
| `self-service` | Install winify itself as a native Windows service (`sc create`, auto-start, recovery) |
| `local-target` | Create the local `winsvc` server entry (no credential — runs in-process) |
| `caddy` | Ensure `caddy.exe`, write the base Caddyfile, install the NSSM Caddy service, open 80/443 |
| `firewall` | `New-NetFirewallRule` for 80/443 |
| `tunnel` | (when enabled) install cloudflared + provision the public ingress |

**Paths** (Windows): root `%ProgramData%\winify`; DB `data\control-center.db`;
tools `tools\`; clones `work\`; installs `apps\`; backups `backups\`; logs `logs\`.

**Commands**: `winify bootstrap [--dry-run] [--remote <winrm-endpoint> --user ...]`.

**Elevation UX**: on first interactive run without admin, winify requests UAC
(`Start-Process -Verb RunAs`). As a service it's already LocalSystem.

---

## 13. Monitoring — agentless (`internal/monitoring`)

No agent. The scheduler polls each registered server over the **same SSH/WinRM
connection** the deployer uses.

**Metrics collected** (both shells emit identical `key=value` lines):
`cpu`, `mem_total`, `mem_used`, `disk_total`, `disk_used`, `uptime`, `load1`, and
per-service `service:<name>=<state>`.

- **Windows/WinRM**: `Get-Counter '\Processor(_Total)\% Processor Time'` (CIM fallback), `Win32_OperatingSystem` for memory/uptime, `Win32_LogicalDisk` for disk, `Get-Service` per watched service.
- **Linux/SSH**: reads `/proc/stat` (CPU delta), `/proc/meminfo`, `df -kP`, `/proc/uptime`, `/proc/loadavg`, `systemctl is-active` per service.

**Persistence & retention**: `metrics` table; retention (default 24h) and history
points (default 200) are config. A failed collection is stored as an
**unreachable** sample with the error — never stale numbers. The dashboard shows a
red `down` pill within `monitoring.timeout_seconds`.

---

## 14. AI assistant — local RAG (`internal/assistant`)

- **Knowledge base**: curated docs in `internal/assistant/docs/*.md` (Setup Guide, Adding a Project/Server, Troubleshooting, FAQ, API) — the same docs linked from the README.
- **Vector store**: **ChromaDB** (`chroma_url`), with an automatic **in-memory** fallback if Chroma is unreachable. Ingestion is idempotent.
- **Generation**: **Ollama** (`ollama_url`, default model `gemma3:1b`, alt `qwen3:1.7b`). Deliberately local — no cloud LLM.
- **Intent routing**: `docs` | `deploy` | `server`. A named project/server plus a live keyword ("cpu", "rollback") routes to live data; "how do I…" routes to docs.
- **Grounding**: doc chunks + **live** deploy history and monitoring samples are put in the prompt. The response includes `sources`, `intent`, `model`, `live`, `docs_used`, `latency_ms`.
- **Graceful degradation**: if Ollama is down, winify returns a **deterministic, grounded answer** (live data and/or the top retrieved chunk) instead of hanging or erroring. This is why the assistant still works during a demo without Ollama installed.

---

## 15. Webhooks (`internal/server/webhook.go`, `internal/deployment/webhook.go`)

- Routes: `POST /webhooks/github/{projectID}`, `POST /webhooks/gitlab/{projectID}`.
- **GitHub**: verifies `X-Hub-Signature-256` (HMAC-SHA256) against the project's encrypted secret; requires `X-GitHub-Event: push`.
- **GitLab**: verifies `X-Gitlab-Token`.
- **Branch match**: if the project has a branch, only pushes to it deploy; otherwise 202 `branch mismatch`.
- **Replay protection**: a delivery key derived from the **authenticated body hash** (not the header, which isn't signed) is recorded in `webhook_deliveries` with a composite PK; duplicates are ignored.
- On success: `202 {"status":"accepted","deployment_id":N}`.
- A concurrent deploy yields `409`; the UI's deploy endpoint points at the in-flight deployment so the drawer can attach to its live log.

---

## 16. GitHub App & notifications (`internal/githubapp`, `internal/notify`)

- **GitHub App**: RS256 **JWT** (9-min expiry, 60s clock skew) → **installation token** → **auto-register/update the repo push webhook** (idempotent `EnsurePushHook`) → **ping** a test delivery.
- **Notifications** (all optional, all **non-fatal**, fired async):
  - generic JSON **webhook**,
  - **Slack** incoming webhook,
  - **GitHub commit statuses** via the App (`winify/deploy`: pending/success/failure) — only when the project is linked to a repo and the event has a full commit SHA.

---

## 17. REST API & tokens (`internal/server/api.go`)

Base `/api/v1`, bearer tokens, scopes `read` / `write`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/servers`, `/servers/{id}` | list/get servers |
| POST/DELETE | `/servers` , `/servers/{id}` | save/delete |
| GET | `/projects`, `/projects/{id}` | list/get projects (redacted) |
| POST/DELETE | `/projects`, `/projects/{id}` | save/delete |
| GET | `/projects/{id}/deployments` | deploy history |
| POST | `/projects/{id}/deploy` | trigger a deploy `{"commit":"","ref":""}` |
| POST | `/projects/{id}/rollback` | roll back |
| GET | `/deployments/{id}` | one deployment |
| GET | `/metrics`, `/servers/{id}/metrics` | monitoring |
| GET | `/audit` | recent audited commands |

- Read-only tokens may only issue GET.
- Tokens are stored **hashed** (`token_hash`), shown once, with `last_used_at` updated best-effort.
- Unknown JSON fields are rejected; bodies capped at 64 KiB.
- Project output redacts `env`/`build_env` values and repo credentials.

---

## 18. Audit log (`internal/deployment/audit.go`, `remote_commands`)

Every remote command winify runs — deploy, rollback, monitor, bootstrap, onboard —
is recorded: **server id, server type, action, deployment id, command, error,
timestamp**. Sensitive payloads (env, build args, binary uploads) are replaced by a
label via `RedactAuditText`/`withAuditRedaction`. The **Audit log** page (and
`GET /api/v1/audit`) exposes it filterable by server, action, time window and a
free-text search, paginated. This is the "what command did the last deploy run?"
answer that CI-only tools can't give.

---

## 19. Data model (SQLite, 21 migrations, 13 tables)

| Table | Key columns |
|---|---|
| `app_meta` | key, value (bootstrap state, etc.) |
| `users` | id, username, password_hash, created_at |
| `sessions` | id, user_id, created_at, expires_at |
| `servers` | id, name, type, host, winrm_*, ssh_*, credential_ref, ssh_key_ref, nssm_path, caddy_path, public_ip, local, services_json, disk_path |
| `projects` | id, name, server_id, strategy, source, repo_url, dockerfile/compose/image, iis_*, service_*, runtime, caddy_mode, branch, domain, port, ports_exposes, ports_mappings, env_json, build_env_json, health_*, project_group, environment, disable_health_check, github_repo, iis_blue_green, webhook_secret_ref |
| `credentials` | name, nonce, ciphertext (AES-256-GCM) |
| `deployments` | id, project_id, target_type, commit_sha, ref, image_tag, status, trigger, log, error, started_at, finished_at |
| `metrics` | server_id, ts, reachable, error, cpu/mem/disk/uptime/load, services_json |
| `remote_commands` | server_id, server_type, action, deployment_id, command, error, executed_at |
| `api_tokens` | id, name, token_hash, scope, created_at, last_used_at |
| `shared_variables` | scope, scope_id, key, value |
| `webhook_deliveries` | provider, project_id, delivery_id, commit_sha, received_at |
| `retired_proxy_domains` | project_id, domain, retired_at |

**Deployment status**: `queued → running → success | failed`.
**Trigger**: `manual | webhook | api | rollback`.

---

## 20. UI / design system (`DESIGN.md`)

A **monochrome operator console**: warm-neutral black surfaces, a fixed left
sidebar, dense panels, hairline borders, one source of emphasis (light ink). No
brand hue — state is carried by value, weight, and one inverted chip.

- Type: **IBM Plex Sans/Mono** + **Newsreader** serif for display headings; full local fallback (works offline).
- Components: sidebar, sticky topbar, cards, tables, status pills, tabs, the **wizard** (guided New Resource flow), the **deploy drawer** (SSE log, `aria-live`).
- Status vocabulary consolidated; failure is the only inverted chip; grayscale success/running/queued.
- Progressive enhancement: works as a plain form without JS; the deploy drawer degrades to a plain POST.
- Motion minimal and disabled under `prefers-reduced-motion`.

Pages: Dashboard, **Deployments**, Monitoring, Assistant, Servers, Projects + Resource (Overview/Deployments/Environment/Settings), Credentials, Shared variables, **Audit log**, Setup, API Tokens, Login/Register.

---

## 21. Configuration reference

Top-level sections (`config.yaml`, all optional; `CC_*` env overrides):

| Section | Key fields |
|---|---|
| `server` | `addr` (listen), `public_url` (webhook callbacks) |
| `database` | `path` |
| `auth` | `admin_user`, `admin_password_hash`, `setup_token`, `cookie_secure`, `session_ttl_hours` |
| `credentials` | `master_key` |
| `files` | `servers`, `projects` (YAML seed) |
| `proxy` | `enabled`, `admin_url`, `server_name`, `public_ip`, `acme_skip` |
| `tunnel` | `enabled`, `provider`, `domain`, `account_id`, `zone_id`, `tunnel_name`, `api_token_ref`, `origin`, `connector_source/url/sha256`, `service_name` |
| `deploy` | `work_dir`, `iis_work_dir`, `iis_backup_dir`, `target_root`, `nssm_source`, `nssm_sha256`, `known_hosts_file`, `allow_insecure_host_key`, `health_*`, `retention_days`, `timeout_minutes` |
| `bootstrap` | `enabled`, `install_dir`, `service_name`, `enable_winrm`, `caddy.*` |
| `github` | `app_id`, `installation_id`, `private_key_ref`, `api_url` |
| `notifications` | `webhook_url`, `slack_webhook_url` |
| `monitoring` | `enabled`, `interval_seconds`, `timeout_seconds`, `retention_hours`, `history_points` |
| `assistant` | `enabled`, `chroma_url`, `collection`, `ollama_url`, `model`, `timeout_seconds`, `top_k` |

Notable env vars: `CC_CONFIG`, `CC_ADMIN_PASSWORD`, `CC_MASTER_KEY`,
`CC_PROXY_ENABLED`, `CC_PROXY_ADMIN_URL`, `CC_TUNNEL_ENABLED`, `CC_TUNNEL_DOMAIN`,
`CC_TUNNEL_ACCOUNT_ID`, `CC_TUNNEL_API_TOKEN_REF`, `CC_DEPLOY_*`, `CC_COOKIE_SECURE`.

---

## 22. CLI subcommands (`cmd/control-center`)

```
control-center serve [-config config.yaml]
control-center bootstrap [-config ...] [--dry-run] [--remote <winrm-endpoint> --user DOMAIN\user]
control-center hash-password                  # bcrypt hash for auth
control-center cred add|list|get <name>       # manage encrypted credentials
```

---

## 23. V1 scope vs V2 (deferred)

**Built (V1):** single admin; one server per target type; Docker requires a
supplied Dockerfile (or compose/image); IIS rollback = local timestamped backups;
repo auto-detection; rollback; blue-green IIS; automatic HTTPS; agentless
monitoring; local assistant; command-level audit; REST API; GitHub App; tunnel.

**Deferred to V2 (explicitly not built):** multi-server orchestration / Docker
Swarm; one-click service catalog (Postgres/Redis add-ons); team/RBAC; automated
off-site backups; private-registry auth; Git OAuth; preview environments.

---

## 24. Testing & CI

- `go test ./...` across `config`, `deployment`, `proxy`, `server`, `models`, `assistant`, `monitoring`, `bootstrap`, `tunnel`, and more — including **Windows-specific** `LocalRunner`/PowerShell tests.
- **CI** (`.github/workflows`):
  - Ubuntu job: `go mod verify`, `gofmt -l`, `go test ./...`, `go vet`, build for Linux **and** `GOOS=windows`, `govulncheck`.
  - Windows job: full test suite on `windows-latest`, PowerShell script parse, and a **smoke test** (build → boot → `/healthz` → login).

---

## 25. The live demo script (5 minutes)

**Setup already done on this machine:** winify as a Windows service on `:8090`,
Caddy, the `cleaner` app (a FastAPI Windows service), and a Cloudflare Tunnel so
`cleaner.edlegend.dev` is publicly reachable over HTTPS despite CGNAT.

1. **Sign in** at `https://winify.edlegend.dev` (or `http://127.0.0.1:8090`).
2. **Dashboard** — recent deployments + server health at a glance.
3. **Projects → cleaner** — show repo, target type (`winsvc`), domain, last deploy.
4. **Click Deploy** — the **deploy drawer** streams over SSE: clone → build → backup → deploy → start → smoke test → proxy register. (If PR #6 hasn't been merged, `main` may be broken — merge it first.)
5. **Open the public URL** `https://cleaner.edlegend.dev` — served through Caddy, reached via the tunnel.
6. **Audit log** — filter to `deploy`; show the actual remote commands (git/nssm/python) with server, action, deployment id.
7. **Monitoring** — CPU/mem/disk/uptime and service state, no agent.
8. **Roll back** — restore the previous timestamped backup; show the new `trigger=rollback` history entry.
9. **Assistant** — ask "What does the last deployment for cleaner do?" — grounded answer with sources (works even without Ollama).

---

## 26. Q&A bank (every question I can think of)

### Category A — "What is this?"
**Q: In one sentence?**
A: A self-hosted deployment platform that owns your build→deploy→proxy→monitor→audit loop and treats Windows/IIS and Docker as equals.

**Q: Isn't this just Coolify/Portainer/Render?**
A: Same spirit, different center of gravity. Those are Linux/Docker-first and assume a public IP. winify treats **Windows services and IIS as first-class**, and adds a **tunnel for CGNAT**, a **local AI assistant**, and a **command-level audit log**.

**Q: Why not just use GitHub Actions?**
A: Actions builds on their runners; it can't manage IIS app pools, install a Windows service, or roll back to a local backup. winify **owns the lifecycle**. A push can trigger it, but CI is optional.

**Q: Is it a dashboard that watches CI?**
A: No. It connects directly to targets, builds, and deploys itself. That's the core principle.

### Category B — "How does it work?"
**Q: How do you reach Windows?**
A: WinRM / PowerShell Remoting (`masterzen/winrm`), NTLM by default. Docker/Linux over SSH (`x/crypto/ssh`).

**Q: Does it install an agent on the target?**
A: No. It uploads `nssm.exe`/`caddy.exe` when needed, but there's no long-running agent. Monitoring reuses the same SSH/WinRM connection.

**Q: What's the deployment state machine?**
A: `queued → running → success | failed`, with `trigger` = manual/webhook/api/rollback. One in-flight deploy per project (a lock keyed by project id).

**Q: How is a deploy triggered?**
A: A signed push webhook (GitHub/GitLab), the UI Deploy button, or the REST API. Webhooks are HMAC-verified and replay-protected.

**Q: How does rollback work?**
A: Docker re-runs the previous image tag (no rebuild); compose re-checks out the previous commit; IIS/winsvc restore the newest timestamped backup on the target. IIS blue-green just repoints the site to the other slot.

**Q: What languages can it deploy?**
A: Anything you can build with a command: Go, .NET, Python, Node, static — with auto-detection — plus any Dockerfile/compose/prebuilt image.

**Q: What does it run on?**
A: One Go binary + SQLite. Windows or Linux. No external database or queue.

### Category C — "Is it secure?"
**Q: How are secrets stored?**
A: AES-256-GCM in SQLite, key from env/file; referenced as `vault:<name>` and decrypted only in memory at connection time. Never logged, never returned by the API.

**Q: SSH host keys?**
A: Fail-closed. You provide a `known_hosts` file; there's a documented dev-only escape hatch.

**Q: How do you avoid command injection?**
A: Validated IDs/domains/paths, single-quoted PowerShell, base64 for file contents, redaction of secrets in audit, and rejection of unknown API fields.

**Q: Talk to any cloud?**
A: No. The assistant is local (ChromaDB + Ollama). The only outbound calls are the ones you configure (GitHub, the tunnel provider, notification sinks).

**Q: Who can log in?**
A: One admin (V1), bcrypt + sessions with Secure/HttpOnly/SameSite cookies. API has scoped read/write bearer tokens (hashed at rest). RBAC is V2.

### Category D — "The CGNAT / public-IP problem"
**Q: My ISP uses CGNAT — can you still give a URL?**
A: Yes. winify provisions a **Cloudflare Tunnel**: it creates the tunnel, points `*.yourdomain` at Caddy, creates the wildcard DNS record, and runs the connector. Public HTTPS, no inbound port needed.

**Q: Why not sslip.io?**
A: sslip.io maps a hostname to an IP; behind CGNAT that IP has no inbound path. It also can't get certs, hence winify serves it HTTP-only. The tunnel is the real answer.

**Q: How do you keep it stable across a live deploy?**
A: The wildcard ingress + DNS are created once at setup, so per-deploy winify only registers the host in Caddy — no provider calls, and the domain never changes.

### Category E — "Reliability"
**Q: What if a deploy fails?**
A: Nothing live is touched until validation passes. On failure winify **restores the previous release** (backup or previous image) and marks the deploy `failed` with the full log. The old version keeps serving.

**Q: What if a health check fails?**
A: The deploy is failed and compensated; Docker additionally dumps `compose ps`/`logs` so the cause (e.g. a container-port mismatch) is visible.

**Q: What if Caddy is down?**
A: winify warns at startup; deploys without a domain still run. Routes require Caddy.

**Q: What if the target is unreachable?**
A: Monitoring marks it `down` with the connection error within the timeout — it never shows stale numbers.

**Q: Is a bad deploy able to take the site down mid-request?**
A: For IIS blue-green, no — the live directory is never overwritten; the swap is a physicalPath repoint. For others, validation-then-backup-then-deploy limits the window.

**Q: Can two people deploy at once?**
A: Per project, no — one in-flight deploy is enforced. (Multi-user/RBAC is V2.)

### Category F — "Operations & scale"
**Q: Does it scale / multi-server?**
A: V1 is one server per target type; multi-server orchestration and Swarm are explicitly V2.

**Q: Backups?**
A: Local timestamped copies for rollback. Off-site/S3 is V2.

**Q: Databases/Redis one-click?**
A: Not in V1 (service catalog deferred). You deploy them like any app.

**Q: Private registries / Git OAuth?**
A: V2. Today: public repos, or SSH URLs you can authenticate at the target level.

**Q: Retention?**
A: Deployments/audit pruned after `deploy.retention_days` (default 90); metrics after `monitoring.retention_hours` (default 24).

### Category G — "The AI assistant"
**Q: Does it use ChatGPT?**
A: No. Local **Ollama** generation + **ChromaDB** retrieval, grounded in winify's own docs and live deploy/monitoring data. No cloud LLM.

**Q: What if Ollama isn't running?**
A: winify still answers from retrieval and live data (deterministic fallback), citing sources. It never hangs or errors on stage.

**Q: Can it answer about my last deploy?**
A: Yes — intent `deploy` pulls live history; intent `server` pulls live metrics.

### Category H — "Engineering choices"
**Q: Why Go?**
A: Single static binary, easy cross-compile, strong concurrency for long deploys and SSE streams, and a rich SSH/WinRM ecosystem. Works on Windows and Linux.

**Q: Why SQLite?**
A: One-file DB, zero ops, pure-Go driver (`modernc.org/sqlite`) so no C toolchain on Windows. PostgreSQL-compatible schema noted for later.

**Q: Why Caddy?**
A: Automatic HTTPS and an admin API winify can drive for per-deploy route registration — no certbot, no config reloads by hand.

**Q: Why no JS framework?**
A: Progressive enhancement keeps it offline-friendly, fast, and debuggable. The deploy drawer uses SSE and degrades to a plain POST.

**Q: How is it tested?**
A: `go test ./...` plus a Windows CI job that runs the full suite, parses the packaging scripts, and smoke-boots the binary (health + login). `govulncheck` runs in CI.

### Category I — "The demo / practical"
**Q: How fast is install?**
A: Drop the binary + config, run `winify bootstrap` once (elevated). It creates dirs, NSSM, Caddy, firewall, the local target — and the tunnel if configured.

**Q: Cost?**
A: Self-hosted; the software is free to run. External costs: your server, a domain, and (optionally) a tunnel provider (Cloudflare's free tier works).

**Q: What's the license?**
A: Not declared in the repo yet — decide before publishing/sharing.

**Q: What's the demo app?**
A: The **AKIRS Data Cleaner** (FastAPI) deployed as a native Windows service, live at `cleaner.edlegend.dev` through Caddy + Cloudflare Tunnel.

### Category J — "Tough / gotcha"
**Q: Isn't exposing an admin panel publicly dangerous?**
A: It is, and it's a choice. Remove the tunnel ingress rule to un-publish it, stop the service to take it down, or put **Cloudflare Access** (email OTP) in front. Keep it loopback/internal for production.

**Q: What if the current branch is broken?**
A: winify deploys exactly what's in the repo — so a broken `main` deploys and fails the health check, then compensates back to the previous release. Fix the repo (or pin a good ref).

**Q: What about Windows service env updates — do removed secrets linger?**
A: No. An empty env explicitly resets NSSM's `AppEnvironmentExtra`.

**Q: Clones made by a different user ("dubious ownership")?**
A: winify runs git with `-c safe.directory=<repo>` so LocalSystem can operate on a user-owned clone.

**Q: What's not done yet?**
A: Multi-server orchestration, RBAC/multi-user, off-site backups, service catalog, private registries, Git OAuth, preview envs, and non-Cloudflare tunnel providers.

---

## 27. Glossary & one-liners

- **Target** — a deploy destination: `winsvc`, `iis`, or `docker`.
- **Project / Resource** — a deployable unit bound to a target.
- **Pipeline** — the ordered remote steps for a target type.
- **Registrar** — the reverse-proxy adapter (Caddy) that maps a domain to an upstream.
- **Artifact** — what a deploy records for rollback: a Docker image tag, a commit, or a Windows backup path.
- **Bootstrap** — idempotent, elevated host provisioning.
- **Vault ref** — `vault:<name>`, an encrypted credential resolved only at connection time.
- **ACME skip** — Caddy hosts served over plain HTTP behind an external TLS terminator.
- **Tunnel** — outbound connector that publishes a public URL for CGNAT hosts.

**Elevator pitch (10s):** "Coolify for Windows and Docker, that works behind CGNAT."

**Elevator pitch (30s):** "winify is a single self-hosted Go binary that deploys
Windows services, IIS sites, and Docker containers, fronts them with automatic
HTTPS, monitors them with no agent, keeps an audit of every command, and runs a
local AI assistant over its own docs. It even gives you a public URL when your ISP
puts you behind CGNAT, via a Cloudflare Tunnel."

---

## 28. Closing statement

> winify proves you don't need a cloud platform or an external CI system to get a
> modern deployment experience. By owning the lifecycle — connecting to targets
> directly, building, deploying, proxying, monitoring, and auditing — it brings
> `git push → live HTTPS URL` to the environments that big tools ignore: **mixed
> Windows/IIS and Docker, self-hosted, and often behind CGNAT.** Windows isn't a
> second-class citizen here, secrets are encrypted and never logged, failures
> explain themselves and roll back safely, and the AI stays local. One binary,
> one operator, full control.

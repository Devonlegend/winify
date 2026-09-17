# Windows Native Services: NSSM + Caddy

Branch: `research/nssm-windows-services` · Status: Phases 1-2 implemented.

Implemented: `winsvc` server type + `Server.NSSMPath/CaddyPath`, `Project.Service*`
fields and `caddy_mode`, migration `0012_windows_service.sql`, the
`windowsServiceTarget` pipeline (`internal/deployment/winservice.go`) with NSSM
bootstrap/upload, install/update, start and rollback, factory + monitoring
wiring, admin validation, API support and UI fields.

Deferred: per-target Caddy **proxy** mode is rejected with a clear error (only
`static` is implemented); see section 3 for the port model that needs a decision
before proxy mode can be added.

## 1. Goal

Give Windows Server targets a deployment path that is as light and fast as the
Linux/Docker path, without depending on IIS. The app runs as a native Windows
service managed by **NSSM**; **Caddy** provides reverse proxy, TLS and static
file serving. IIS is kept only for sites that genuinely need it (Windows auth,
classic ASP.NET, existing IIS apps); native + Caddy becomes the default for new
Windows resources.

## 2. Research: NSSM

**What it is.** Non-Sucking Service Manager — a single native `.exe` (~300 KB,
public domain, no installer, no runtime dependencies) that wraps any
executable/script as a real Windows service with supervision. It registers
*itself* as the service binary, so the exe must live at a stable path and must
not be moved or deleted after a service is installed.

**Configuration we care about**

| Setting | Purpose |
| --- | --- |
| `Application`, `AppDirectory`, `AppParameters` | what to run, from where, with which args |
| `AppStdout` / `AppStderr` | redirect app logs to files |
| `AppRotate` / `AppRotateBytes` / `AppRotateOnline` | log rotation |
| `AppEnvironmentExtra` | inject env vars (additive; keeps system env) |
| `AppExit Default Restart` | restart on crash |
| `AppThrottle`, `AppRestartDelay` | restart backoff (avoids tight crash loops) |
| `Start SERVICE_AUTO_START` | start on boot |
| `ObjectName` | service account |
| `AppStopMethodConsole/Window/Threads` | graceful shutdown sequence |

CLI: `nssm install|set|start|stop|restart|remove <name> [args]`; reset a value
with `nssm reset <name> <parameter>`.

**Caveats**
- Stable `2.24` is from 2014. On Windows 10 / Server 2016+ use pre-release
  `2.24-101-g897c7ad` (2017) or newer, or set `AppNoConsole=1`, to avoid a
  known console-window startup bug.
- Unmaintained but stable. It installs itself as an Event Log source, so keep a
  single canonical copy per host to avoid confusion.

**Alternatives considered**

| Tool | Weight | Config | Maintenance |
| --- | --- | --- | --- |
| **NSSM** | ~300 KB, no deps | CLI/registry | stale (2017) |
| WinSW | tens of MB, needs .NET 4.6.1+ | XML file | active |
| `sc.exe create` | built-in | none | n/a — only works if the exe implements the SCM handler API; no supervision, no log redirect |
| srvany / shawl | small | varies | legacy / niche |

NSSM wins on the stated goal (lightweight, fast, no runtime). We keep the
wrapper behind a small interface so WinSW can be added later without touching
the pipeline.

**NSSM vs Docker on Windows Server.** Windows containers require a heavy engine,
GB-scale base images and Mirantis Container Runtime (Docker Desktop/WSL2 are not
supported on Server); container starts are multi-second. A wrapped native binary
starts in well under a second and carries no engine, image or registry overhead.
For a native Go/.NET/Node service, NSSM + Caddy is the lightweight equivalent of
the Docker path.

## 3. Target architecture

```
                 public internet
                        │  https://app.example.com
                        ▼
        ┌───────────────────────────────┐
        │  Central Caddy (control-center)│  admin API, auto TLS/ACME
        └───────────────┬───────────────┘
                        │ reverse_proxy
                        ▼
        ┌──────────────────────────────────────────┐
        │  Windows Server (winsvc target, WinRM)     │
        │                                            │
        │  NSSM ──► app.exe  (Windows service, :PORT)│
        │  NSSM ──► caddy.exe (optional, :LOCAL)     │  static / SPA / local proxy
        └──────────────────────────────────────────┘
```

- **Central Caddy** (existing, `internal/proxy/caddy.go`) owns public hostnames,
  ACME/auto-TLS and routes `domain → windows-host:port`. This already works for
  the Docker path; the Windows path reuses it unchanged.
- **Per-target Caddy** (optional, also run under NSSM) serves static/SPA files
  and can reverse-proxy locally. It listens on a **non-80/443 internal port**
  (e.g. `:8080`); central Caddy terminates public TLS and proxies to it. Avoids
  two ACME clients fighting over ports 80/443 on the same box.
- **IIS** stays as a target type; not used for the native path.

## 4. Data model changes

`internal/config/config.go`
- add `ServerTypeWindowsService = "winsvc"`.

`internal/config/entities.go` — `Server` (reuse all WinRM fields):
- `NSSMPath` (default `C:\control-center\tools\nssm.exe`)
- `CaddyPath` (optional; default `C:\control-center\tools\caddy.exe`)

`Project` (native path):
- `ServiceName`, `ServiceExe` (path within the build output), `ServiceArgs`
- `ServiceWorkDir`, `ServiceBuildCommand`, `ServiceLogDir`, `ServiceAccount`
- `CaddyMode` (`none` | `proxy` | `static`) for the per-target Caddy
- reuse `Env`, `Port`, `HealthPath`, `Domain`, `Branch`, `RepoURL`

Deploy-time directories (config `DeployConfig`, Windows-style defaults):
- `ServiceWorkDir` root `C:\control-center`
- `ServiceBackupDir` `C:\control-center\backups`
- `NSSMToolsDir` `C:\control-center\tools`

## 5. Deploy pipeline

New `internal/deployment/winservice.go`, mirroring `iis.go` structure (PowerShell
script builders + a `Target` implementation), reusing `syncRepoScript`,
`buildScript`, `copyScript`, `backupScript`, `smokeScript` where possible.

1. **Sync** — clone/fetch repo, checkout revision.
2. **Build** — optional `ServiceBuildCommand` (`dotnet publish`, `go build`,
   `npm ci && npm run build`).
3. **Validate before touching live** — nssm present at `NSSMPath`; `ServiceExe`
   exists in the build output; port free or owned by the target service.
4. **Backup** — timestamped copy of the live install dir (robocopy).
5. **Stop** — `nssm stop <name>` (tolerate "not installed" on first deploy).
6. **Deploy files** — copy build output into the install dir.
7. **Install/update service**
   - absent: `nssm install <name> <exe>` then `nssm set` the params below
   - present: `nssm set` Application / AppDirectory / AppParameters
   - always: `AppEnvironmentExtra` (redacted in audit), `AppStdout`/`AppStderr`
     into `ServiceLogDir`, `AppExit Default Restart`, `AppThrottle`,
     `AppRotateFiles 1`, `Start SERVICE_AUTO_START`, `ObjectName` if set
8. **Start** — `nssm start <name>`, poll `Get-Service` until `Running`.
9. **Health check** — reuse `smokeScript` against `127.0.0.1:<port>`.
10. **Per-target Caddy** (if `CaddyMode != none`) — push Caddyfile, `caddy reload`;
    ensure the Caddy service is installed via NSSM on first use.

## 6. NSSM bootstrap (platform uploads the binary)

On first deploy to a `winsvc` target, upload `nssm.exe` to `NSSMToolsDir`:

- The WinRM runner uses a **max envelope size of 153600 bytes**
  (`winrm.NewParameters("PT120S", "en-US", 153600)` in
  `internal/deployment/winrm_client.go`). A ~300 KB exe base64-encoded (~400 KB)
  does not fit one command, so upload in **chunks**: base64-encode, split into
  ~140 KB pieces, `[IO.File]::AppendAllText` each, then decode once to
  `nssm.exe` with `[IO.File]::WriteAllBytes`.
- Verify a pinned **SHA-256** after assembly; abort on mismatch.
- Store the 64-bit build only (Server targets are x64).
- Never re-upload if the hash already matches (idempotent, fast redeploys).
- The tools path is permanent — NSSM's service `ImagePath` points at it.
- Caddy binary bootstrapped the same way when per-target Caddy is enabled.

_Alternative if chunking proves awkward:_ host the binary at a token-protected
control-center endpoint and `Invoke-WebRequest` it from the target. Rejected for
v1 because it adds an HTTP endpoint and assumes target→control-center
reachability; chunked upload reuses the existing session.

## 7. Rollback

Mirrors IIS: discover the latest timestamped backup on the target, stop the
service, restore the install dir, ensure the service points at the restored exe,
start, health-check. No dependency on deploy history.

## 8. What is reused for free

- **Monitoring** — `windowsScript` already reports Windows service status via
  `Get-Service`; add the NSSM service name to `Server.Services`.
- **Audit** — the WinRM runner is already wrapped by `WithAuditRecorder`.
- **Proxy** — central Caddy registration and `targetHost` need no change.
- **Health** — `smokeScript` / `healthTiming` reused as-is.
- **UI** — server type dropdown, project form fields, and the resource wizard
  gain the `winsvc` option; templates follow the IIS form pattern.

## 9. Phased implementation

- **Phase 1 — MVP:** config/model constants, `winservice.go` target, factory
  branch in `target.go`, NSSM chunked bootstrap + hash check, install/run a
  prebuilt exe, start, health check.
- **Phase 2 — Build & rollback:** `ServiceBuildCommand`, timestamped backup and
  rollback, env injection (audit-redacted), log rotation.
- **Phase 3 — Per-target Caddy:** optional Caddy service via NSSM, static/SPA
  `file_server`, local reverse proxy on an internal port.
- **Phase 4 — Product surface:** server/project validation, wizard fields,
  monitoring service names, docs + assistant ingestion, E2E on a Windows VM.

## 10. Open questions / deferred

- Server type name: `winsvc` is provisional (alternatives: `windows`).
- Service account default: `LocalSystem` vs a dedicated virtual/service account.
- Port allocation / collision handling across multiple services on one host.
- Caddy on target: ship a generated Caddyfile per app vs drive the admin API.
- Private-registry/artifact sources for prebuilt native binaries (still v2).

## 11. Testing

- Unit: fake `Runner` asserting the generated PowerShell (mirror
  `internal/deployment/iis_test.go`), including chunk assembly and hash
  verification.
- Model: `config` load/round-trip for the new fields.
- E2E: a Windows Server VM target in `E2E-CHECKLIST.md` — install service, verify
  it survives reboot, deploy a second revision, roll back.

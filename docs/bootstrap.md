# Zero-touch Bootstrap

Status: **implemented** (phases 1-5) · Branch: `research/nssm-windows-services`

Implemented: `internal/bootstrap` engine (idempotent steps + `app_meta` state),
steps `dirs`, `nssm`, `winrm`, `self-service`, `caddy`, `firewall`, `target`;
`winify bootstrap` (local, `--dry-run`, `--remote`); native Windows service
support (`internal/service`); first-run trigger; Setup page (status, Run,
local-target form); remote provisioning over WinRM; local/remote target
auto-creation. Binaries are supplied via `deploy.nssm_source` and
`bootstrap.caddy.{source,url}` with optional SHA-256 pins.

**winify is the name of the entire control center** (server, UI, deploy engine,
targets). This document plans how winify provisions a Windows host on install so
it is immediately usable, with no manual setup.

## Decisions (locked)

| Decision | Choice |
| --- | --- |
| Install model | **Both** — self-provision the local Windows box, and offer remote bootstrap |
| Trigger | **Automatic on first start**, idempotent and re-runnable |
| Local target auth | **Prompt once** for an account/password; winify stores it encrypted |
| Local `winsvc` server | **Created by winify automatically** (not manual) |
| Install directory | Windows convention: **`%ProgramData%\winify`** (remote bootstrap also uses this when the controller is Linux) |
| WinRM | **Explicitly enabled** on a trusted management profile (default off) |
| Binaries (NSSM/Caddy) | Downloaded and verified against **pinned SHA-256** |
| Scope | winify-as-service, WinRM+firewall, NSSM, Caddy+service, dirs+permissions, local `winsvc` server |

## Core idea: one engine, two transports

A single set of idempotent steps runs either **locally** (PowerShell via
`os/exec`) or **remotely** (the existing `deployment.WinRMRunner`). Self-provision
and remote-provision share all logic.

```
internal/bootstrap/
  bootstrap.go      // Bootstrap{steps, runner, recorder}
  runner.go         // Runner: localRunner | winrmRunner
  steps_windows.go  // the ordered steps
  state.go          // app_meta: bootstrap.<step> = done|error + timestamp
```

```go
type Step interface {
    Name() string
    Check(ctx) (done bool, err error)   // cheap idempotency probe
    Apply(ctx) error                    // idempotent
    Privileged() bool
}
```

## Path model

On Windows, defaults are computed from `%ProgramData%` (`os.Getenv("ProgramData")`)
rather than hard-coded, so a service account has the right ACLs and the data
survives upgrades:

| Purpose | Path |
| --- | --- |
| Root | `%ProgramData%\winify` |
| Database | `%ProgramData%\winify\data\control-center.db` |
| Tools | `%ProgramData%\winify\tools\{nssm.exe,caddy.exe}` |
| Work (clones) | `%ProgramData%\winify\work` |
| Apps (installs) | `%ProgramData%\winify\apps` |
| Backups | `%ProgramData%\winify\backups` |
| Logs | `%ProgramData%\winify\logs` |

Linux keeps its own defaults (`/var/lib/winify`, `/opt/winify`). Config values
still override every path.

## Steps (Windows)

| # | Step | What it does | Idempotency probe |
| --- | --- | --- | --- |
| 1 | `dirs` | Create the tree above; grant the service account modify rights | `Test-Path` each dir |
| 2 | `nssm` | Ensure `tools\nssm.exe` (local `deploy.nssm_source` → else download + hash check) | `Get-FileHash` matches pin |
| 3 | `winrm` | `Enable-PSRemoting -Force`; set network profile Private if needed; firewall rule | service running + listener on 5985 |
| 4 | `self-service` | Install **winify itself as a native Windows service** (`x/sys/windows/svc` + `sc.exe create`), auto-start, recovery | `sc query winify` |
| 5 | `caddy` | Ensure `tools\caddy.exe` (pinned hash); write base Caddyfile; install Caddy service (NSSM); open 80/443 | service + config present |
| 6 | `local-target` | **Create the local `winsvc` server** (no credential) | server `local` exists |

Step 4 uses native service support (`golang.org/x/sys/windows/svc`, already an
indirect dependency), so winify does not depend on NSSM to run itself — NSSM is
only used for user workloads and for Caddy.

### Step 6 detail — the auto-created local server

The local target uses a **local executor**: for the host winify runs on, the
deploy and monitoring paths run PowerShell in-process (`deployment.LocalRunner`)
instead of over WinRM. WinRM always needs a credential, so this removes the
prompt (and the 401s) entirely; when winify runs as a service it is LocalSystem,
already elevated.

Create the server record with `local: true`:

| Field | Value |
| --- | --- |
| `id` | `local` |
| `name` | the hostname |
| `type` | `winsvc` |
| `local` | `true` |
| `host` | `127.0.0.1` (proxy upstream) |
| `nssm_path` | `%ProgramData%\winify\tools\nssm.exe` |
| `public_ip` | optional (for sslip.io domains) |

Remote servers keep the WinRM credential path. WinRM is still enabled on the
host (for remote access), but the local target does not depend on it.

Result: right after install, the box is already a deployable target.

## Trigger and elevation

- New subcommand: `winify bootstrap [--dry-run] [--remote <winrm-endpoint>]`.
- On `serve` start, if Windows and bootstrap is incomplete:
  - **Running as a service (LocalSystem)** → already elevated → run in the
    background.
  - **Interactive, not elevated** → never silently UAC; show a **Setup** page
    with a *Run bootstrap* button that launches an elevated `winify bootstrap`
    (`Start-Process -Verb RunAs`) and streams step status.
- State lives in `app_meta` (`bootstrap.<step> = done|error` + timestamp), so a
  failed step re-runs without redoing completed ones.

## Installer entry point

`install.ps1` installs a local release or an HTTPS download. Remote downloads
require an explicit `-Sha256` digest; the script verifies the NSSM archive before
extracting it. For a local payload, pass `-Sha256` as well when distributing it
through an artifact store. The installer generates and prints a one-time setup
token for a new configuration. This is "install on the server and it is ready".

## Remote bootstrap

- `winify bootstrap --remote http://host:5985/wsman --user …` and a UI action run
  the same steps over WinRM, reusing the chunked binary upload already built for
  NSSM.
- Chicken-and-egg: the remote must already accept WinRM. Provide a copy-paste
  one-time snippet (`Enable-PSRemoting -Force`); a WMI/PsExec fallback is
  deferred.

## UI

- New **Setup** page under Settings: per-step status (done/pending/failed), *Run
  bootstrap*, *Re-run*, live log.
- First-run shows the same checklist right after registration.
- Nav label added alongside Credentials / Shared variables.

## Config

New `bootstrap:` section:

```yaml
bootstrap:
  enabled: true
  install_dir: ""            # default %ProgramData%\winify (Windows)
  service_name: "winify"
  enable_winrm: true         # toggle for the WinRM step
  caddy:
    enabled: true
    nssm_url: "..."          # pinned
    caddy_url: "..."         # pinned
    sha256: ""               # optional overrides
```

Reuses `deploy.nssm_source` / `deploy.nssm_sha256`.

## Phasing

1. Engine + local steps (`dirs`, `nssm`, `winrm`) + `bootstrap` command + state +
   read-only Setup page.
2. Native self-service install + elevation UX (UAC button) + first-run trigger.
3. Caddy provisioning + service + firewall + proxy auto-config.
4. Guided `local-target` credential prompt + automatic server creation.
5. Remote bootstrap over WinRM + one-time enable snippet.

## Security

- Privileged steps require elevation; every apply is written to the audit log.
- Binary downloads are verified against pinned SHA-256.
- `--dry-run` reports intended changes without applying.
- Auto-enabling WinRM and creating services are logged and configurable.
- Credentials are never logged; the prompted password goes straight into the
  encrypted store.

## Open questions

1. Binaries: download with pinned hashes (chosen) — confirm no requirement for
   fully offline/embedded installs.
2. Remote bootstrap when WinRM is off on the remote: accept the one-time manual
   snippet for v1, or invest in a WMI/PsExec fallback?
3. Upgrade path: should bootstrap also handle "update winify to a new version"
   (stop service, replace binary, restart), or is that a separate command?

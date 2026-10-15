---
title: Add a server
description: Onboard Windows automatically or register Docker, winsvc, and IIS servers manually.
---

# Add a server

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

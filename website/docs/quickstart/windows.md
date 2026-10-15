---
title: Windows quickstart
description: Install winify on Windows and get a native Windows service live.
---

# Windows quickstart

This is the recommended path. A Windows host provisions itself: directories, NSSM, winify as a native Windows service, Caddy, firewall rules, and a local `winsvc` target.

## Prerequisites

- Windows 10/11 or Windows Server with Administrator access.
- A `winify.exe` release binary (or `go build -o winify.exe ./cmd/control-center` on Go 1.27+).
- Ports 80/443 reachable from the internet **only if** you want public Let's Encrypt certificates. Behind CGNAT, use the [tunnel](../how-to/tunnel-cgnat.md) instead.
- Optional for HTTPS names: a DNS `A` record (or wildcard) pointing at the Caddy host.

## 1. Install

Quick install from an **elevated** PowerShell (downloads the latest GitHub
Release, verifies SHA-256, installs + starts the `winify` service):

```powershell
irm https://devonlegend.github.io/winify/install.ps1 | iex
```

Pin a version or change the port:

```powershell
$env:WINIFY_VERSION = "v0.1.0"
irm https://devonlegend.github.io/winify/install.ps1 | iex
```

Manual path — from an **elevated** PowerShell with a local binary:

```powershell
.\winify.exe bootstrap
```

What it does, in order (`dirs` → `nssm` → `winrm` → `self-service` → `local-target` → `caddy` → `firewall` → `tunnel`):

| Step | Effect |
| ---- | ------ |
| `dirs` | Creates `%ProgramData%\winify\{tools,work,apps,backups,logs,data}` |
| `nssm` | Ensures `tools\nssm.exe` from `deploy.nssm_source`, SHA-256 verified |
| `winrm` | `Enable-PSRemoting -Force` **only if** `bootstrap.enable_winrm: true` (default `false`) |
| `self-service` | Installs winify itself as the `winify` Windows service, auto-start with recovery |
| `local-target` | Creates the local `winsvc` server entry (no credential, in-process PowerShell) |
| `caddy` | Ensures `caddy.exe`, writes base config, installs `winify-caddy`, opens 80/443 |
| `tunnel` | Only when `tunnel.enabled: true` — provisions Cloudflare ingress |

Preview without changing anything:

```powershell
.\winify.exe bootstrap --dry-run
```

State is recorded in `app_meta` (`bootstrap.<step>`), so re-runs skip done steps and repair failed ones.

## 2. Configure

Copy the example config on first run:

```powershell
Copy-Item config.example.yaml config.yaml
```

Minimum production checks in `config.yaml`:

```yaml
server:
  addr: "127.0.0.1:8080"   # keep loopback until registration is complete
proxy:
  enabled: true
  admin_url: "http://127.0.0.1:2019"
bootstrap:
  enabled: true
```

All fields are optional; built-in defaults apply when the file is absent. Every section also has a `CC_*` env override — see [Configuration reference](../reference/config.md).

## 3. Start and register

```powershell
.\winify.exe serve -config config.yaml
```

On a fresh database:

1. Open `http://127.0.0.1:8080`.
2. The first-run page asks for the **one-time setup token** printed in the service log (the installer also prints it).
3. Create the admin username + password. The page closes permanently after the first account exists.

Keep the loopback listener until the admin account exists. Do not expose the UI publicly before registration.

For headless installs, seed the admin without a browser:

```powershell
$env:CC_ADMIN_PASSWORD = "a-long-unique-password"
.\winify.exe serve -config config.yaml
```

Or pre-hash: `.\winify.exe hash-password` → put the bcrypt hash in `auth.admin_password_hash`.

## 4. Deploy the first service

1. Go to **Projects → New**. The wizard detects your repo (`go.mod`, `.csproj`, `requirements.txt`, `package.json`, `index.html`) and proposes a build/run plan with confidence + evidence.
2. Pick the `local` winsvc server created by bootstrap.
3. Set `service_name`, `service_exe`, `service_work_dir`, and `domain`.
4. Click **Deploy**. Watch the drawer stream over SSE: clone → runtime → build → backup → copy → install service → start → smoke test → proxy register.
5. Open `https://<your-domain>`.

## Next

- [First deploy walkthrough](./first-deploy.md)
- [Add a server](../how-to/add-server.md)
- [Tunnel for CGNAT](../how-to/tunnel-cgnat.md) if ports 80/443 cannot reach you

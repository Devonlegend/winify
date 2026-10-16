---
title: Production install
description: Run winify itself hardened, loopback-first, service, firewall, Caddy admin.
---

# Production install

## 1. Keep loopback until registered

```yaml
server:
  addr: "127.0.0.1:8080"
```

The default listener is loopback-only. The database and master key are created on first run, and a one-time setup token is printed in the service log. The token is required on the registration page. **Do not expose the listener until registration is complete.**

## 2. Install as a service (Windows)

```powershell
.\winify.exe bootstrap
```

Installs `winify` (the app), `winify-caddy`, and optionally `winify-tunnel` via the service manager with auto-start and recovery. On Linux, run behind your process supervisor with the same config.

## 3. Harden the edges

- Caddy admin (`proxy.admin_url`, default `http://127.0.0.1:2019`) is unauthenticated, keep it on loopback or a protected management network. Never publish 2019 publicly.
- `auth.cookie_secure: true` in production (localhost browsers still accept Secure cookies).
- `deploy.known_hosts_file` populated; `allow_insecure_host_key: false`.
- `bootstrap.enable_winrm: false` unless the host is on a trusted management network.
- `deploy.target_root` bounds destructive Windows copies; `allow_external_target_paths: false` unless separately governed.
- Pin binary hashes (`deploy.nssm_sha256`, `bootstrap.caddy.sha256`); mismatch aborts the deploy/provision.

## 4. Expose deliberately

- `server.addr: "0.0.0.0:8080"` only behind a trusted reverse proxy or firewall.
- `server.public_url: "https://cc.example.com"` for correct webhook callback URLs.
- The admin panel is sensitive. Keep it loopback/internal, or put Cloudflare Access (email OTP) in front. The tunnel step does **not** auto-route the UI.

## 5. What to back up

- SQLite file (`database.path`, default `data/control-center.db`).
- `data/master.key` (or `CC_MASTER_KEY` / `credentials.master_key` source): without it, credentials cannot be decrypted.
- `config.yaml` (no secrets if you use `vault:` refs + env).

See [Backup and restore](./backup-restore.md).

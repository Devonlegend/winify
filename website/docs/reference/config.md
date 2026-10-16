---
title: Configuration reference
description: Every config.yaml section with defaults and CC_ environment overrides.
---

# Configuration reference

`config.yaml` is optional; built-in defaults apply when absent. Copy `config.example.yaml` to `config.yaml` and edit. Every section also reads `CC_*` env vars (env wins for the mapped keys).

## server

| Key | Default | Env | Notes |
| --- | ------- | --- | ----- |
| `server.addr` | `127.0.0.1:8080` | `CC_ADDR` | Keep loopback until registration is complete; `0.0.0.0:8080` only behind a trusted proxy/firewall |
| `server.public_url` | derive from request | `CC_PUBLIC_URL` | Externally reachable base URL for webhook callbacks, e.g. `https://cc.example.com` |

## database

| Key | Default | Env |
| --- | ------- | --- |
| `database.path` | `data/control-center.db` | `CC_DB_PATH` |

Created on first run if missing.

## auth

| Key | Default | Env | Notes |
| --- | ------- | --- | ----- |
| `auth.admin_user` | `admin` | `CC_ADMIN_USER` | |
| `auth.admin_password_hash` | `""` | (use `CC_ADMIN_PASSWORD` plaintext, hashed at startup) | Generate with `control-center hash-password` |
| `auth.setup_token` | generated + logged | `CC_SETUP_TOKEN` | One-time first-run token when no admin exists |
| `auth.cookie_secure` | `true` | `CC_COOKIE_SECURE` | Keep true in production; localhost browsers still accept Secure cookies |
| `auth.session_ttl_hours` | `12` | - | |

## credentials

| Key | Default | Env |
| --- | ------- | --- |
| `credentials.master_key` | generated `data/master.key` | `CC_MASTER_KEY` (base64 32 bytes) |

## files

| Key | Default | Env | Notes |
| --- | ------- | --- | ----- |
| `files.servers` | `servers.yaml` | `CC_SERVERS_FILE` | Imported only when tables are empty |
| `files.projects` | `projects.yaml` | `CC_PROJECTS_FILE` | Imported only when tables are empty |

## proxy

| Key | Default | Env | Notes |
| --- | ------- | --- | ----- |
| `proxy.enabled` | `true` | `CC_PROXY_ENABLED` | `false` = deploys run, no public URL |
| `proxy.admin_url` | `http://127.0.0.1:2019` | `CC_PROXY_ADMIN_URL` | Unauthenticated, loopback/management net only |
| `proxy.server_name` | `srv0` | - | |
| `proxy.public_ip` | `""` | `CC_PROXY_PUBLIC_IP` | Expected Caddy host address for DNS warnings |
| `proxy.acme_skip` | `["*.sslip.io"]` | - | Host patterns served HTTP without a certificate |

## tunnel

| Key | Default | Env |
| --- | ------- | --- |
| `tunnel.enabled` | `false` | `CC_TUNNEL_ENABLED` |
| `tunnel.provider` | `cloudflare` | - |
| `tunnel.domain` | `""` | `CC_TUNNEL_DOMAIN` |
| `tunnel.account_id` | `""` | `CC_TUNNEL_ACCOUNT_ID` |
| `tunnel.zone_id` | `""` (resolved) | - |
| `tunnel.tunnel_name` | `winify` | - |
| `tunnel.api_token_ref` | `vault:cloudflare-api` | `CC_TUNNEL_API_TOKEN_REF` |
| `tunnel.origin` | `http://localhost:80` | - |
| `tunnel.connector_source/url/sha256` | `""` | - |
| `tunnel.service_name` | `winify-tunnel` | - |

## deploy

| Key | Default | Env | Notes |
| --- | ------- | --- | ----- |
| `deploy.work_dir` | `/opt/control-center` | `CC_DEPLOY_WORKDIR` | Docker/Linux clone parent |
| `deploy.iis_work_dir` | `C:\ProgramData\winify\work` | `CC_DEPLOY_IIS_WORKDIR` | |
| `deploy.iis_backup_dir` | `C:\ProgramData\winify\backups` | `CC_DEPLOY_IIS_BACKUP_DIR` | |
| `deploy.target_root` | `C:\ProgramData\winify\apps` | `CC_DEPLOY_TARGET_ROOT` | Destructive copies must stay below here |
| `deploy.allow_external_target_paths` | `false` | `CC_ALLOW_EXTERNAL_TARGET_PATHS` | Only for separately governed roots |
| `deploy.nssm_source` | `tools/nssm.exe` | `CC_DEPLOY_NSSM_SOURCE` | Empty disables upload (NSSM must pre-exist) |
| `deploy.nssm_sha256` | `""` | `CC_DEPLOY_NSSM_SHA256` | Pinned hash; mismatch aborts |
| `deploy.known_hosts_file` | `""` | `CC_DEPLOY_KNOWN_HOSTS` | Required in production; fail-closed |
| `deploy.allow_insecure_host_key` | `false` | `CC_ALLOW_INSECURE_SSH` | Dev-only escape hatch |
| `deploy.health_timeout_seconds` | `60` | - | |
| `deploy.health_interval_seconds` | `3` | - | |
| `deploy.retention_days` | `90` | `CC_DEPLOY_RETENTION_DAYS` | |
| `deploy.timeout_minutes` | `30` | `CC_DEPLOY_TIMEOUT_MINUTES` | Whole-deploy bound |

## bootstrap

| Key | Default | Env |
| --- | ------- | --- |
| `bootstrap.enabled` | `true` | `CC_BOOTSTRAP_ENABLED` |
| `bootstrap.install_dir` | `%ProgramData%\winify` / `/var/lib/winify` | `CC_BOOTSTRAP_INSTALL_DIR` |
| `bootstrap.service_name` | `winify` | - |
| `bootstrap.enable_winrm` | `false` | `CC_BOOTSTRAP_ENABLE_WINRM` |
| `bootstrap.caddy.enabled` | `false` | `CC_BOOTSTRAP_CADDY_ENABLED` |
| `bootstrap.caddy.source` | `""` | `CC_BOOTSTRAP_CADDY_SOURCE` |
| `bootstrap.caddy.url` | `""` | `CC_BOOTSTRAP_CADDY_URL` |
| `bootstrap.caddy.sha256` | `""` | `CC_BOOTSTRAP_CADDY_SHA256` |
| `bootstrap.caddy.admin` | `127.0.0.1:2019` | - |

## github

| Key | Default | Env |
| --- | ------- | --- |
| `github.app_id` | `""` | `CC_GITHUB_APP_ID` |
| `github.installation_id` | `0` | `CC_GITHUB_INSTALLATION_ID` |
| `github.private_key_ref` | `vault:github-app-key` | `CC_GITHUB_PRIVATE_KEY_REF` |
| `github.api_url` | `https://api.github.com` | `CC_GITHUB_API_URL` |

## notifications

| Key | Default | Env |
| --- | ------- | --- |
| `notifications.webhook_url` | `""` | `CC_NOTIFY_WEBHOOK_URL` |
| `notifications.slack_webhook_url` | `""` | `CC_NOTIFY_SLACK_WEBHOOK_URL` |

## monitoring

| Key | Default | Env |
| --- | ------- | --- |
| `monitoring.enabled` | `true` | `CC_MONITOR_ENABLED` |
| `monitoring.interval_seconds` | `30` | `CC_MONITOR_INTERVAL` |
| `monitoring.timeout_seconds` | `15` | `CC_MONITOR_TIMEOUT` |
| `monitoring.retention_hours` | `24` | `CC_MONITOR_RETENTION_HOURS` |
| `monitoring.history_points` | `200` | - |

## assistant

| Key | Default | Env |
| --- | ------- | --- |
| `assistant.enabled` | `true` | `CC_ASSISTANT_ENABLED` |
| `assistant.chroma_url` | `http://127.0.0.1:8000` | `CC_CHROMA_URL` |
| `assistant.collection` | `control-center-docs` | `CC_ASSISTANT_COLLECTION` |
| `assistant.ollama_url` | `http://127.0.0.1:11434` | `CC_OLLAMA_URL` |
| `assistant.model` | `gemma3:1b` | `CC_ASSISTANT_MODEL` |
| `assistant.timeout_seconds` | `60` | `CC_ASSISTANT_TIMEOUT` |
| `assistant.top_k` | `3` | `CC_ASSISTANT_TOP_K` |

Extra CLI-only env: `CC_CONFIG` (config path), `CC_ADMIN_PASSWORD` (seed admin), `CC_REMOTE_WINRM_USER` / `CC_REMOTE_WINRM_PASSWORD` (remote bootstrap).

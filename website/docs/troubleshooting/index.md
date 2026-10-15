---
title: Troubleshooting
description: Fix SSH, WinRM, build, health, proxy, monitoring, and assistant failures.
---

# Troubleshooting

Failures explain themselves: error banner above the relevant log, `down` pills with the connection error, never stale data.

## SSH connection failures (Docker)

Deploy fails immediately → SSH is the problem. Check `ssh_host`, `ssh_port` (22), `ssh_user`, and that `ssh_key_ref` exists and holds a valid PEM/OpenSSH key. `no key found` = re-add via `control-center cred add server-002-ssh`.

Production: `deploy.known_hosts_file` must be set (fail-closed). `allow_insecure_host_key` is dev-only.

## WinRM connection failures (Windows)

Check `winrm_endpoint` (`https://host:5986/wsman` recommended), `winrm_user`, and `credential_ref`. `ntlm` default; `basic` only over HTTPS. HTTP needs `winrm_insecure: true`.

`connection refused`/timeout = listener unreachable: firewall 5985/5986, `winrm quickconfig`. Monitoring `down` with the same error confirms it.

## Docker build and compose failures

- `dockerfile_path` is relative to the repo root; target must fetch base images.
- `docker compose` missing → install the Compose plugin.
- Port conflict → another container uses the project's `port` / mapping.

## IIS validation, app pool, service failures

Validation runs before live is touched, so these are safe:

- `source directory not found` → `iis_source_subdir` / build command.
- `web.config` XML failure → build produced malformed XML.
- `app pool not found` → correct `iis_app_pool` (`Get-ChildItem IIS:\AppPools`).
- `service not found` → correct or remove `iis_service`.

## Health check and smoke test failures

Post-start GET to `http://127.0.0.1:<port><health_path>` failed → app did not start or listens elsewhere. Confirm container listening (Docker) or pool started + binding matches `port` (IIS). Full output is in the deploy log. Workers with no HTTP endpoint should set `disable_health_check: true`.

## Reverse proxy and TLS

Registration failure → `proxy.admin_url` unreachable, or `domain` does not resolve to `proxy.public_ip`. `proxy.enabled: false` = no URL by design, not an error. Tunnel-fronted / `sslip.io` hosts are intentionally HTTP (in `acme_skip`).

## Monitoring shows down

SSH/WinRM open failed or the metrics command failed/timed out (`monitoring.timeout_seconds`). Same connection as deploys — if deploys work, read the card error for the metrics-specific cause.

## Assistant is unavailable

Needs Ollama for generation, optionally ChromaDB for retrieval. ChromaDB down → in-process fallback, still answers. Ollama down → verbatim grounded answer from docs/live data. Check `assistant.ollama_url` and that `assistant.model` is pulled.

---
title: CLI reference
description: serve, bootstrap, hash-password, and cred subcommands with flags.
---

# CLI reference

```text
control-center serve [-config config.yaml]
control-center bootstrap [-config ...] [--dry-run] [--remote <winrm-endpoint> --user DOMAIN\user]
control-center hash-password
control-center cred add|list|get <name>
```

Binary is `control-center` (Linux) / `control-center.exe` / `winify.exe` (Windows release name). Bare invocation defaults to `serve`.

## serve

```bash
./control-center serve -config config.yaml
```

- `--config` defaults to `CC_CONFIG` or `config.yaml`.
- Graceful shutdown on SIGINT/SIGTERM or service stop (bounded read/header/idle timeouts, deploy workers drained up to 30s).
- On boot: prune sessions, reconcile `running` deployments left by a crash to `failed`, prune history by retention.

## bootstrap

```powershell
.\winify.exe bootstrap --dry-run
.\winify.exe bootstrap
.\winify.exe bootstrap --remote http://host:5985/wsman --user 'DOMAIN\user'
```

| Flag | Notes |
| ---- | ----- |
| `--config` | Config path |
| `--dry-run` | Report intended changes without applying |
| `--remote` | WinRM endpoint for remote provisioning |
| `--user` | Remote user (or `CC_REMOTE_WINRM_USER`) |
| `--id`, `--name` | Remote server id/name overrides |

Remote password via `CC_REMOTE_WINRM_PASSWORD` or an interactive prompt. Must run elevated locally (UAC or LocalSystem). See [Bootstrap hosts](../how-to/bootstrap.md).

## hash-password

```bash
./control-center hash-password
```

Prompts for a password, prints a bcrypt hash for `auth.admin_password_hash`. Prefer `CC_ADMIN_PASSWORD` (hashed at startup) over storing a hash in a file.

## cred

```bash
./control-center cred add server-002-ssh
./control-center cred list
./control-center cred get server-002-ssh
```

- `cred add <name>` reads the value from stdin (pipe a key file or paste a secret) and stores it AES-256-GCM encrypted.
- `cred list` shows names only, never values.
- `cred get` decrypts for provisioning scripts; avoid logging its output.

Examples:

```bash
./control-center cred add server-002-ssh < ~/.ssh/id_ed25519
./control-center cred add github-app-key < app-key.pem
echo -n "s3cret" | ./control-center cred add proj-001-webhook
```

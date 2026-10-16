---
title: Credentials
description: AES-256-GCM store, vault refs, master key handling, and SSH/WinRM trust.
---

# Credentials

Secrets are never stored in plaintext. Add from the **Credentials** tab or CLI; reference by name; they decrypt only in memory at connection time and never appear in logs or API responses.

```bash
./control-center cred add server-002-ssh
./control-center cred list   # names only
```

Reference from a server (`ssh_key_ref` / `credential_ref`) and from a project (`webhook_secret_ref` / `git_credential_ref`) as `vault:<name>`:

```yaml
ssh_key_ref: "vault:server-002-ssh"
webhook_secret_ref: "vault:proj-001-webhook"
git_credential_ref: "vault:proj-001-git"   # private repos: deploy key or HTTPS token
```

## Store

- **AES-256-GCM** in SQLite (`credentials`: nonce + ciphertext). The credential **name is the AEAD additional data** — ciphertext cannot be moved between names; renaming requires re-encryption.
- Master key from `CC_MASTER_KEY` (base64 32 bytes), `credentials.master_key`, or generated into `<data dir>/master.key` on first use (`0600`, symlink-rejected).
- Project env values are write-only (`[redacted]`); submitting the marker preserves existing values.
- Env values and binary payloads are marked sensitive — audit records a label, not the payload.

## SSH trust

Host-key verification is **fail-closed**: populate `deploy.known_hosts_file` from a trusted provisioning source. Emergency `deploy.allow_insecure_host_key` is dev-only — never in production.

## WinRM trust

- `winrm_transport`: `ntlm` (default) or `basic` (basic requires HTTPS).
- HTTP requires explicit `winrm_insecure: true`; self-signed HTTPS should prefer a trusted cert over the same flag.
- The WinRM account is highly privileged (install services, write filesystem) — scope it to a trusted management network.

## Tunnel / GitHub App tokens

- Cloudflare API token: Account `Cloudflare Tunnel:Edit` + Zone `DNS:Edit` on the tunnel domain. Stored as `vault:cloudflare-api`.
- GitHub App private key: stored as `vault:github-app-key` via `cred add < app-key.pem`.
- Connector tokens are bearer credentials written only to a protected token file, never the log.

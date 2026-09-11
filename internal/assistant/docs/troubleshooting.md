# Troubleshooting

## SSH connection failures (Docker)

If a deploy fails immediately, the SSH connection is the problem. Check that
`ssh_host`, `ssh_port` (default 22) and `ssh_user` are correct and that the
credential named by `ssh_key_ref` exists and contains a valid private key.
Errors such as "no key found" mean the stored credential is not a PEM or OpenSSH
private key; re-add it with `control-center cred add server-002-ssh`.

For production, set `deploy.known_hosts_file` so host keys are verified. When it
is empty the platform logs a warning and does not verify host keys.

## WinRM connection failures (IIS)

Check that `winrm_endpoint` is correct (`https://host:5986/wsman` for HTTPS,
`http://host:5985/wsman` for HTTP), that `winrm_user` is set, and that the
credential named by `credential_ref` exists. The platform supports `ntlm`
(default) and `basic` transports. For self-signed WinRM certificates set
`winrm_insecure: true`.

A "connection refused" or timeout means the WinRM listener is unreachable:
verify the firewall allows 5985/5986 and that `winrm quickconfig` has been run.
If monitoring reports the server as down, the same connection is failing.

## Docker build and compose failures

The build runs `docker build` on the target using `dockerfile_path` relative to
the repo root. Confirm the Dockerfile exists there and that the target can fetch
base images. If `docker compose` is unavailable, install the Compose plugin.
Port conflicts usually mean another container already uses the project's `port`.

## IIS validation, app pool and service failures

Validation runs before anything live is changed, so these failures are safe:

- "source directory not found": check `iis_source_subdir` and the build command.
- "web.config is valid XML" failure: the build produced malformed XML.
- "IIS app pool not found": correct `iis_app_pool`; confirm with
  `Get-ChildItem IIS:\AppPools`.
- "service not found": correct or remove `iis_service`.

## Health check and smoke test failures

After starting the app the platform requests
`http://127.0.0.1:<port><health_path>` on the target. A failure usually means the
app did not start or listens on a different port. Confirm the container is
listening (Docker) or the app pool started and the site binding matches `port`
(IIS). The full output is in the deploy log.

## Reverse proxy and TLS

On a successful deploy the app is registered with Caddy, which issues and renews
the certificate. If registration fails, check that `proxy.admin_url` is
reachable and that the project's `domain` resolves to the target host. When
`proxy.enabled` is false, no public URL is registered and that is not an error.

## Monitoring shows a server as down

The platform could not open its SSH or WinRM connection, or the remote metrics
command failed. The card shows the specific error. Metrics use the same
connection as deploys, so if deploys work but monitoring is down, read the error
text on the card. A "collection timed out" message means the target did not
respond within `monitoring.timeout_seconds`.

## The assistant is unavailable

The assistant needs Ollama for generated answers and optionally ChromaDB for
retrieval. If ChromaDB is unreachable it falls back to an in-process store and
still answers. If Ollama is unreachable it returns the retrieved documentation
or live data verbatim instead of a generated answer. Check `assistant.ollama_url`
and that the model named by `assistant.model` has been pulled.

# Docker Troubleshooting Guide

Docker deployments run over SSH against a Linux target. The pipeline is: clone
or fetch the repo on the target, build the image from the project's Dockerfile,
write a generated `docker-compose.yml`, run `docker compose up -d`, and run an
HTTP health check before the deploy is considered successful.

## SSH connection failures

If the deploy fails immediately, the SSH connection is the problem. Check that
`ssh_host`, `ssh_port` (default 22) and `ssh_user` are correct and that the
credential named by `ssh_key_ref` exists and contains a valid private key.
Errors such as "no key found" mean the stored credential is not a PEM/OpenSSH
private key. Re-add it with `control-center cred add server-002-ssh`.

For production, set `deploy.known_hosts_file` so host keys are verified. When it
is empty the platform logs a warning and does not verify host keys.

## docker build fails

The build runs `docker build` on the target using `dockerfile_path` relative to
the repo root. Confirm the Dockerfile exists at that path and that the target
has network access to fetch base images. The build log is captured in the
deployment history and shown on the Deployment tab.

## docker compose up fails

The platform generates a compose file with the built image, the published port
and the project's `env` values. If `docker compose` is unavailable, install the
Compose plugin on the target. Port conflicts usually mean another container is
already bound to the project's `port`.

## Health check fails

After `compose up`, the platform polls
`http://127.0.0.1:<port><health_path>` on the target until the timeout. A failed
health check marks the deploy as failed and leaves the previous container state
in place where possible. Verify the container is listening on the published port
and that `health_path` returns a 2xx or 3xx response.

## Reverse proxy and TLS

On a successful deploy the app is registered with Caddy, which issues and renews
the Let's Encrypt certificate automatically. If `proxy.enabled` is false the
deploy still runs but no public URL is registered. If registration fails, check
that Caddy's admin API (`proxy.admin_url`) is reachable and that the project's
`domain` resolves to the target host.

## Rollback

Rollback reuses the previous successful image tag and brings it back up with
Docker Compose without rebuilding. It appears in history with trigger
`rollback`.

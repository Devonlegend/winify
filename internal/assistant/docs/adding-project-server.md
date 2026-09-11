# Adding a Project or Server

You can add and edit servers, projects and credentials entirely from the
dashboard (the **Servers**, **Projects** and **Credentials** tabs). The database
is the source of truth; `servers.yaml` and `projects.yaml` are imported only
once, when the tables are empty (a fresh install). After that, manage everything
in the UI.

## Adding a server

Open the **Servers** tab and fill in the form.

For a Docker target choose `type: docker` and set `ssh_host`, `ssh_port`,
`ssh_user` and the SSH key credential ref (for example
`vault:server-002-ssh`). For an IIS target choose `type: iis` and set
`winrm_endpoint`, `winrm_user`, `winrm_transport` (`ntlm` or `basic`),
`winrm_insecure`, and the WinRM credential ref. The `type` selects the deploy
pipeline.

Optionally set `services` (systemd units or Windows service names to report in
Monitoring) and `disk_path` (the volume to report).

## Adding a Docker project

Open the **Projects** tab, fill in the form and pick the Docker server:

- `id` and `name`
- `repo_url` — the git repository
- `branch` — the branch a push must target (default `main`)
- `domain` — the public hostname Caddy will serve over HTTPS
- `port` — the port the app listens on
- `health_path` — a path that returns 2xx/3xx (for example `/healthz`)
- `dockerfile_path` — where the Dockerfile lives in the repo
- `webhook_secret_ref` — the name of an encrypted credential used to verify the
  push webhook
- `env` — optional `KEY=VALUE` lines injected into the container

The pipeline clones the repo on the target, builds the image from
`dockerfile_path`, runs it with Docker Compose on `port`, and health-checks
`health_path` before treating the deploy as successful.

## Adding an IIS project

Same form, but pick the IIS server and fill in `iis_physical_path` and
`iis_app_pool` (plus optional `iis_service`, `iis_build_command` and
`iis_source_subdir`). The pipeline validates the build and `web.config`, stops
the service and app pool, backs up the live directory, copies the new files,
restarts the service and recycles the app pool, then smoke-tests the site.

## Credentials

Open the **Credentials** tab to add or delete secrets. Values are encrypted at
rest with AES-256-GCM and are never shown again after saving. Reference a
credential by name from a server (`ssh_key_ref` / `credential_ref`) or a project
(`webhook_secret_ref`).

## Triggering a deploy

A push to the project's `branch` triggers a deploy through the signed webhook:

    POST /webhooks/github/{projectID}
    POST /webhooks/gitlab/{projectID}

Requests with a missing or invalid signature are rejected with HTTP 401, and
pushes to other branches are ignored. You can also start a deploy directly from
the **Projects** tab with the **Deploy** button — useful for the first deploy or
a retry.

## Verifying a deployment

Watch the **Deployment** tab: each attempt shows its status, commit, artifact
and full log. A successful deploy records the live URL. If the target is a
Docker host, `docker ps` on the target shows the running container; for IIS, the
site should respond on its port.

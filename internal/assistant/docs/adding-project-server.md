# Adding a Project or Server

You can add and edit servers, projects and credentials entirely from the
dashboard (the **Servers**, **Projects** and **Credentials** tabs). The database
is the source of truth; `servers.yaml` and `projects.yaml` are imported only
once, when the tables are empty (a fresh install). After that, manage everything
in the UI.

## Adding a server

Open the **Servers** tab. The fastest path is **Onboard a Windows server**: give
the WinRM endpoint, user and password, and the platform provisions directories,
NSSM and Caddy on the target and creates the server record for you.

For a manual entry, choose the type and fill in the form:

- **winsvc — Windows native service (recommended)**. The default for Windows
  servers: your app runs as a Windows service via NSSM and Caddy provides HTTPS
  and static files. Set `winrm_endpoint`, `winrm_user`, `winrm_insecure` and the
  WinRM credential ref; NSSM/Caddy paths default to `C:\ProgramData\winify\tools`.
- **docker — Linux/Docker over SSH**. Set `ssh_host`, `ssh_port`, `ssh_user` and
  the SSH key credential ref (for example `vault:server-002-ssh`).
- **iis — IIS (advanced/legacy)**. Only for apps that need ASP.NET Framework,
  Windows authentication, classic ASP, or an existing IIS site.

The `type` selects the deploy pipeline.

Optionally set `services` (systemd units or Windows service names to report in
Monitoring) and `disk_path` (the volume to report).

## Adding a Docker project

Open the **Projects** tab, fill in the form and pick the Docker server. A Docker
project has a **deploy source**:

- **dockerfile** — build the image from the repo. Set `repo_url`, `branch` and
  `dockerfile_path` (default `Dockerfile`).
- **compose** — deploy the repository's own compose file. Set `repo_url`,
  `branch` and `compose_path` (default `docker-compose.yml`). Project `env`
  values are written to a `.env` file next to the compose file.
- **image** — run a prebuilt registry image. Set `image` (for example
  `nginx:1.27`) and `container_port` if the image listens on a different port
  than `port`.

All sources also use:

- `id` and `name`
- `domain` — the public hostname Caddy will serve over HTTPS
- `port` — the published host port (also used for the health check and proxy)
- `container_port` — the port inside the container; defaults to `port`
- `health_path` — a path that returns 2xx/3xx (for example `/healthz`)
- `disable_health_check` — set to `true` to skip the post-deploy health check
  entirely (workers or cron jobs with no HTTP endpoint)
- `webhook_secret_ref` — the name of an encrypted credential used to verify the
  push webhook (dockerfile and compose sources)
- `env` — optional `KEY=VALUE` lines

For dockerfile and compose sources the pipeline clones the repo on the target.
The dockerfile source builds the image, then runs it with Docker Compose on
`port` and health-checks `health_path`. The compose source runs
`docker compose up -d --build`. The image source pulls the image and runs it
with a generated compose file. In all cases the deploy is only successful if
the health check passes.

## Adding a Windows native-service project

Pick the winsvc server and fill in `service_name`, `service_exe` and
`service_work_dir` (plus optional build command, source subdir, log directory
and Caddy mode). The pipeline syncs the repo, builds if configured, validates
before touching the live install, backs up the current directory, copies files,
installs/updates the NSSM service, starts it and smoke-tests the port. A static
site (`caddy_mode: static`) needs no service name or executable — the per-target
Caddy serves the files.

## Adding an IIS project (advanced/legacy)

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

### Automatic GitHub webhook registration

Instead of registering the webhook manually, configure the GitHub App in
`config.yaml`:

    github:
      app_id: "123456"
      installation_id: 98765432
      private_key_ref: "vault:github-app-key"

The App needs **Administration: read/write** on the repositories it manages and
must be installed on them. Store the App's private key with
`control-center cred add github-app-key < app-key.pem`, and set
`server.public_url` to the externally reachable control-center URL. Then open a
project, enter `owner/repo` under **Connect webhook**, and winify generates the
secret, stores it encrypted and creates (or updates) the push hook. The manual
flow (`webhook_secret_ref` + registering the URL yourself) still works for
GitLab or unmanaged repos.

## Verifying a deployment

Watch the **Deployment** tab: each attempt shows its status, commit, artifact
and full log. A successful deploy records the live URL. If the target is a
Docker host, `docker ps` on the target shows the running container; for IIS, the
site should respond on its port.

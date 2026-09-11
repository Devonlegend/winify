# Platform User Guide

The DevOps Control Center is a self-hosted deployment platform for environments
that mix Windows IIS and Docker. It connects directly to target servers over SSH
(Linux/Docker) and WinRM/PowerShell Remoting (Windows/IIS), builds and deploys
applications, fronts each deployment with a reverse proxy and automatic TLS, and
polls metrics over the same connections.

## Servers and projects

Targets are declared in `servers.yaml`; deployable units are declared in
`projects.yaml`. Both files are synced into SQLite at startup. A project is bound
to a server by `server_id`, and the bound server's `type` (`docker` or `iis`)
selects the deploy pipeline.

A Docker project needs `repo_url`, `dockerfile_path`, `branch`, `domain`,
`port` and `health_path`. An IIS project needs `repo_url`, `branch`, `domain`,
`port`, `iis_physical_path`, `iis_app_pool` and optionally `iis_service`,
`iis_build_command` and `iis_source_subdir`.

## Credentials

Secrets are never stored in plaintext. Add an encrypted credential by name:

    control-center cred add server-002-ssh

Servers and projects reference credentials by name using `credential_ref`,
`ssh_key_ref` or `webhook_secret_ref`. The value is decrypted only in memory at
connection time and is never written to a log.

## Triggering a deployment

Deployments are triggered by a push webhook, not by a dashboard button. Point
GitHub at `POST /webhooks/github/{projectID}` with a secret, or GitLab at
`POST /webhooks/gitlab/{projectID}`. Requests with a missing or invalid
signature are rejected with HTTP 401. A push to the project's configured
`branch` starts the pipeline; pushes to other branches are ignored.

## Rolling back a deployment

Rollback restores the previous known-good version of a project. On the
Deployment tab, find the project and click **Roll back**. The button is shown
only when at least two successful deployments exist.

For a Docker project, rollback reuses the image tag from the previous
successful deployment (no rebuild) and brings it back up with Docker Compose.
For an IIS project, rollback restores the most recent timestamped backup under
the configured `iis_backup_dir` and restarts the service and application pool.
In both cases the action is recorded in deploy history as a deployment with
trigger `rollback`.

Rollback is also what you use after a bad release: pick the project, click
**Roll back**, and the previous version becomes live again.

## Deploy history

Every attempt is recorded with its project, target type, commit, artifact,
status, timestamps and full log. The Deployment tab shows history per project,
distinguishing Docker and IIS with a type badge. The JSON API is available at
`GET /api/projects/{projectID}/deployments` and `GET /api/deployments/{id}`.

## Monitoring

The Monitoring tab shows live CPU, memory and disk gauges plus a historical
chart per server, pulled over the existing SSH/WinRM connections with no agent
to install. Polling interval, timeout and retention are configured under the
`monitoring` section. If a server's connection is down the card is marked
**down** and shows the connection error instead of stale numbers. Service state
is reported for the names listed in the server's `services` field.

## Assistant

The Assistant tab answers questions using a small curated knowledge base (this
guide, the IIS and Docker troubleshooting guides, and the FAQ) plus live
deployment and monitoring data. It runs entirely locally with ChromaDB and
Ollama and never calls a cloud service.

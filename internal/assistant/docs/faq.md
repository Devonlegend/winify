# FAQ

## How do I roll back a deployment?

Open the Deployment tab, find the project, and click **Roll back**. The button
appears once a project has at least two successful deployments. Docker rollback
reuses the previous successful image tag without rebuilding; IIS rollback
restores the most recent timestamped backup and restarts the service and app
pool. The rollback is recorded in deploy history.

## How do I add a new server?

Add an entry to `servers.yaml` with an `id`, `name`, `type` (`docker` or `iis`),
the connection details, and credential references, then restart the
control-center. The inventory is synced into SQLite at startup.

## How do I add a new project?

Add an entry to `projects.yaml` with an `id`, `server_id`, `repo_url`, `branch`,
`domain`, `port`, `health_path` and a `webhook_secret_ref`, plus the
pipeline-specific fields, then restart. Point your Git provider's webhook at
`/webhooks/github/{projectID}` or `/webhooks/gitlab/{projectID}`.

## Where are credentials stored?

Credentials are encrypted with AES-256-GCM and stored in SQLite. The master key
comes from `CC_MASTER_KEY` or a generated `data/master.key` file. Plaintext
secrets are never written to disk or logs; they are decrypted only in memory
when a connection is opened.

## How do I trigger a deployment?

Push to the project's configured branch. The signed webhook starts the
pipeline automatically. There is intentionally no manual deploy button in v1.

## Why is a server shown as down in Monitoring?

The platform could not open its SSH or WinRM connection, or the remote metrics
command failed. The card shows the specific error. Common causes are an
unreachable host, a firewall blocking SSH/WinRM, or a missing/invalid
credential. Metrics are pulled over the same connection used for deploys, so if
deploys work but monitoring is down, check the connection error text.

## Does the assistant call the internet?

No. The assistant uses a local ChromaDB vector store and a local Ollama model.
It answers from the curated guides and from live deployment and monitoring data
in the local database. It cites whether it used documentation or live data.

## How do I change the assistant model?

Set `assistant.model` in `config.yaml` (or `CC_ASSISTANT_MODEL`) to any model
you have pulled in Ollama, for example `qwen3:1.7b` or `gemma3:1b`.

## What is deferred to a later version?

Multi-server orchestration, a one-click service catalog, role-based access
control, off-site backups and automatic language/framework detection are all
deferred to v2 and are not part of the current release.

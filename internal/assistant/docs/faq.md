# FAQ

## How do I roll back a deployment?

Open the Deployment tab, find the project, and click **Roll back**. The button
appears when there is a prior successful release to restore (or after a failed
attempt when one successful release exists). Docker rollback
reuses the previous successful image tag without rebuilding; IIS rollback
restores the most recent timestamped backup and restarts the service and app
pool. The rollback is recorded in deploy history.

## How do I trigger a deployment?

Push to the project's configured branch. The signed webhook starts the pipeline
automatically; you can also use **Deploy** on the project page for a manual
deployment of the configured branch.

## Where are credentials stored?

Credentials are encrypted with AES-256-GCM and stored in SQLite. The master key
comes from `CC_MASTER_KEY` or a generated `data/master.key` file. Plaintext
secrets are never written to disk or logs; they are decrypted only in memory
when a connection is opened.

## Does the assistant call the internet?

No. The assistant uses a local ChromaDB vector store and a local Ollama model.
It answers from the curated guides and from live deployment and monitoring data
in the local database, and it cites whether it used documentation or live data.

## How do I change the assistant model?

Set `assistant.model` in `config.yaml` (or `CC_ASSISTANT_MODEL`) to any model
pulled in Ollama, for example `gemma3:1b` or `qwen3:1.7b`. Smaller models are
faster but may follow instructions less reliably.

## How do I add a new server or project?

Edit `servers.yaml` or `projects.yaml` and restart the control-center. See the
"Adding a Project or Server" guide for the required fields for Docker and IIS.

## Why is a server shown as down in Monitoring?

The platform could not open its SSH or WinRM connection, or the remote metrics
command failed. The card shows the specific error. Common causes are an
unreachable host, a firewall blocking SSH/WinRM, or a missing or invalid
credential.

## What is deferred to a later version?

Multi-server orchestration, a one-click service catalog, role-based access
control, off-site backups and automatic language/framework detection are all
deferred to v2 and are not part of the current release.

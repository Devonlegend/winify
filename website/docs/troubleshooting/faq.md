---
title: FAQ
description: Short answers to the questions every operator asks.
---

# FAQ

## How do I roll back?

Deployment tab → **Roll back** (appears with a prior successful release). Docker reuses the previous image tag without rebuilding; IIS/winsvc restore the newest timestamped backup (blue-green repoints). Recorded as `trigger=rollback`.

## How do I trigger a deployment?

Push to `branch` (signed webhook), **Deploy** button, or `POST /api/v1/projects/{id}/deploy`.

## Where are credentials stored?

AES-256-GCM in SQLite; key from `CC_MASTER_KEY` or generated `data/master.key`. Decrypted only in memory at connection time. Never logged or API-returned.

## Does the assistant call the internet?

No. Local ChromaDB + Ollama over these guides and live DB data, with cited sources. No cloud LLM.

## How do I change the assistant model?

`assistant.model` (or `CC_ASSISTANT_MODEL`) to any pulled Ollama model, e.g. `gemma3:1b` or `qwen3:1.7b`.

## How do I add a server or project?

**Servers/Projects** tabs (database is authoritative). `servers.yaml` / `projects.yaml` seed only a fresh install. See [Add a server](../how-to/add-server.md).

## Why is a server down in Monitoring?

SSH/WinRM open failed or the metrics command failed. Card shows the exact error, unreachable host, firewall, bad credential, or timeout.

## What is deferred to V2?

Multi-server orchestration/Swarm, service catalog, RBAC, off-site backups, private-registry auth, Git OAuth, preview environments. Not in this release by design.

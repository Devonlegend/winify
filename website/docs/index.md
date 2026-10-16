---
slug: /
title: Winify docs
description: Install winify, ship Windows and Docker apps to HTTPS, and operate the platform.
sidebar_position: 0
---

# Winify docs

Self-hosted deployments for environments that mix Windows and Docker.
Git push → build → deploy → live HTTPS URL → audit. One Go binary, no agent,
local AI assistant.

## Install

```powershell
irm https://winify.space/install.ps1 | iex
```

```bash
curl -fsSL https://winify.space/install.sh | sh
```

## Start here

- [Introduction](./intro.md) — what winify owns, and what V1 deliberately omits.
- [Windows quickstart](./quickstart/windows.md) — bootstrap a Windows host end to end.
- [Docker quickstart](./quickstart/docker.md) — connect a Linux/Docker host over SSH.
- [First deploy](./quickstart/first-deploy.md) — push → drawer → HTTPS → rollback.

## Operate

- [Concepts](./concepts/architecture.md) — architecture, targets, lifecycle, proxy/TLS, monitoring, assistant.
- [How-to guides](./how-to/add-server.md) — servers, projects, webhooks, rollback, bootstrap, tunnel, notifications.
- [Reference](./reference/config.md) — `config.yaml`, `CC_*` env, CLI, REST API, audit.
- [Security](./security/credentials.md) — credential store, transport trust, hardening.
- [Troubleshooting](./troubleshooting/index.md) — failure matrix and FAQ.

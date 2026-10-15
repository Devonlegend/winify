---
id: intro
title: Introduction
sidebar_position: 1
description: What DevOps Control Center is, what it owns, and what it deliberately does not do.
---

# Introduction

DevOps Control Center (`winify`) is a **self-hosted deployment platform for environments that mix Windows and Docker**. It owns the deployment lifecycle: it connects directly to target servers, builds and deploys on a signed webhook or a manual action, fronts each deployment with a reverse proxy and automatic TLS, and polls metrics over the same connections.

It is **not** a dashboard that watches an external CI system. GitHub Actions is at most an optional trigger, never the engine.

## What it does

- Manages **servers, projects, and encrypted credentials** from one dashboard; deploy from the UI or on a signed push webhook.
- Deploys to three target types over the same lifecycle: **Windows native service (`winsvc`)**, **IIS (`iis`)**, and **Docker/Linux (`docker`)**.
- Exposes a scoped **REST API at `/api/v1`** for automation and CI.
- Verifies push webhooks per-project with HMAC and keeps **deploy history with rollback**.
- Registers every successful deploy with **Caddy for automatic HTTPS** (Let's Encrypt, no certbot step).
- Polls **CPU, memory, disk, uptime, and service state** over the existing SSH/WinRM connections — no agent to install.
- Runs a **local assistant** (ChromaDB + Ollama) grounded in these guides plus live deploy/monitoring data. No cloud LLM.
- Encrypts credentials with **AES-256-GCM**; never logs or returns them.
- Records a **command-level audit log** of every remote command.

## The operator

A single self-hosting operator who runs Windows and Linux/Docker workloads and wants one place to ship and monitor them. They arrive mid-task — ship an app, check whether a deploy succeeded, add a server — not to browse. V1 is **single admin, no RBAC**.

## What it does not do (V1 scope)

- No multi-server orchestration / Docker Swarm.
- No one-click service catalog (Postgres/Redis add-ons).
- No team / role-based access control.
- No automated off-site backups (IIS rollback is local timestamped copies only).
- No private-registry auth or Git OAuth.
- No preview environments.

If you need those, track them as V2. Do not work around their absence with manual database edits.

## Install in one line

Windows (elevated PowerShell — downloads the latest GitHub Release, verifies
SHA-256, installs + starts the `winify` service):

```powershell
irm https://winify.space/install.ps1 | iex
```

Linux (systemd — installs to `/usr/local/bin/winify`, writes
`/etc/winify/config.yaml`, starts the service):

```bash
curl -fsSL https://winify.space/install.sh | sh
```

Prefer a manual build? `go build -o winify ./cmd/control-center` on Go 1.27+,
then [`winify bootstrap`](./quickstart/windows.md). Every installer path ends
at the same first-run registration (loopback-first + one-time setup token).

## How to read these docs

- **Quickstart** — go from zero to a live HTTPS URL.
- **Concepts** — targets, lifecycle states, proxy/TLS/tunnel, monitoring, assistant.
- **How-to** — task guides (add a server, wire a webhook, roll back).
- **Reference** — exact `config.yaml` keys, `CC_*` env vars, CLI flags, API routes.
- **Operations** — run winify itself in production (install, backup, upgrade).
- **Security** — credential store, transport trust, web hardening.
- **Troubleshooting / FAQ** — failure matrices that are never silent.

## Next

Start with [Quickstart: Windows](./quickstart/windows.md) if your host is Windows, or [Quickstart: Docker](./quickstart/docker.md) for a Linux/Docker target. If you only have five minutes, go straight to [First deploy](./quickstart/first-deploy.md).

---
title: Environment variables
description: Project env, build env, shared variables, and template expansion.
---

# Environment variables

Three layers, resolved in order. Secret values are write-only (`[redacted]` in UI/API) and redacted in logs/audit.

| Layer | Scope | Where |
| ----- | ----- | ----- |
| Project `env` | Running workload | Project form → Environment tab; `POST /api/v1/projects` `env` |
| Project `build_env` | Build only | Same form; Docker build args / build-command environment |
| Shared variables | Group or environment | **Variables** tab; `scope` + `scope_id` + `key` |

## Template expansion

Before connecting, the Deployer expands:

- `{{project.KEY}}`: this project's env
- `{{environment.KEY}}`: shared variables for the project's environment

Use them in commands, paths, and compose files to avoid duplicating secrets per project.

## Docker specifics

- `compose` source: project `env` is written to a `.env` file next to the compose file on the target.
- `dockerfile` source: `build_env` becomes `--build-arg` entries (values redacted in audit).

## winsvc specifics

Service environment is set via NSSM `AppEnvironmentExtra`. An empty env **explicitly resets** it, so removed secrets do not survive a redeploy.

## Rules

- Never put secrets in `servers.yaml` / `projects.yaml`: use `vault:<name>` refs.
- Binary payloads and env values are marked sensitive; audit stores a label, not the payload.
- API list/get responses redact `env`/`build_env` values and repo credentials.

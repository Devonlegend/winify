---
title: REST API
description: Bearer-token API at /api/v1, servers, projects, deploys, metrics, audit.
---

# REST API

Base ` /api/v1`, bearer tokens, scopes `read` / `write`. The dashboard uses session auth; the API uses a bearer token. Do not confuse with the session-auth JSON under `/api/*` used by UI polling.

Send on every request:

```text
Authorization: Bearer <token>
```

Create tokens on the **Tokens** tab. The value is shown once and stored only as a hash. `read` = `GET` only. `write` is global and administrator-equivalent: it can change any server/project and trigger privileged deployments.

## Endpoints

```text
GET    /api/v1/servers
GET    /api/v1/servers/{id}
POST   /api/v1/servers                 # create or update
DELETE /api/v1/servers/{id}

GET    /api/v1/projects
GET    /api/v1/projects/{id}
POST   /api/v1/projects                # create or update
DELETE /api/v1/projects/{id}

GET    /api/v1/projects/{id}/deployments?limit=50
POST   /api/v1/projects/{id}/deploy    # body optional: {"commit":"","ref":""}
POST   /api/v1/projects/{id}/rollback

GET    /api/v1/deployments/{id}
GET    /api/v1/metrics
GET    /api/v1/servers/{id}/metrics?limit=200
GET    /api/v1/audit?limit=100
```

## Examples

List projects:

```bash
curl -H "Authorization: Bearer $TOKEN" https://cc.example.com/api/v1/projects
```

Trigger a deploy (empty commit deploys the project's branch):

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}' https://cc.example.com/api/v1/projects/my-app/deploy
```

Create or update a project:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "my-app",
    "name": "My App",
    "server_id": "server-002",
    "repo_url": "https://github.com/you/my-app",
    "dockerfile_path": "Dockerfile",
    "branch": "main",
    "domain": "myapp.example.com",
    "port": 8080,
    "health_path": "/healthz",
    "webhook_secret_ref": "vault:my-app-webhook",
    "git_credential_ref": "vault:my-app-git"
  }' \
  https://cc.example.com/api/v1/projects
```

Read metrics and audit:

```bash
curl -H "Authorization: Bearer $TOKEN" https://cc.example.com/api/v1/servers/server-002/metrics?limit=50
curl -H "Authorization: Bearer $TOKEN" https://cc.example.com/api/v1/audit?limit=20
```

## Status codes and limits

- Deploy/rollback return `202 Accepted` with `{deployment_id}`; poll `GET /api/v1/deployments/{id}` for `status` + `log`.
- Second deploy while one runs for the same project → `409`.
- Read-only token on a mutating endpoint → `403`. Missing/invalid token → `401`.
- `DELETE /servers/{id}` with dependent projects → `409`.
- Bodies capped at 64 KiB; unknown JSON fields are rejected.
- `source` is `dockerfile` | `compose` | `image`. `image` needs `image` (+ optional `ports_exposes`/`ports_mappings`); others need `repo_url`.
- Project output redacts `env`/`build_env` values and repo credentials. Request/response bodies use the same `snake_case` fields as `servers.yaml` / `projects.yaml`.

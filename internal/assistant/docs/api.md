# REST API

The control-center exposes a token-authenticated REST API at `/api/v1` for
automation and CI. The dashboard uses session authentication; the API uses a
bearer token.

## Tokens

Create a token on the **Tokens** tab. The value is shown once and stored only as
a hash; revoke it from the same page. A token has a scope:

- **read** — `GET` requests only.
- **write** — `GET`, `POST` and `DELETE`. It is intentionally global and
  administrator-equivalent: it can change any server/project and trigger
  privileged deployments. Do not issue it to untrusted automation.

Send it on every request:

    Authorization: Bearer <token>

## Endpoints

    GET    /api/v1/servers
    GET    /api/v1/servers/{id}
    POST   /api/v1/servers                 (create or update)
    DELETE /api/v1/servers/{id}

    GET    /api/v1/projects
    GET    /api/v1/projects/{id}
    POST   /api/v1/projects                (create or update)
    DELETE /api/v1/projects/{id}

    GET    /api/v1/projects/{id}/deployments?limit=50
    POST   /api/v1/projects/{id}/deploy    (body optional: {"commit":"","ref":""})
    POST   /api/v1/projects/{id}/rollback

    GET    /api/v1/deployments/{id}
    GET    /api/v1/metrics
    GET    /api/v1/servers/{id}/metrics?limit=200
    GET    /api/v1/audit?limit=100

## Examples

List projects:

    curl -H "Authorization: Bearer $TOKEN" https://cc.example.com/api/v1/projects

Trigger a deploy (empty commit deploys the project's branch):

    curl -X POST -H "Authorization: Bearer $TOKEN" \
         -H "Content-Type: application/json" \
         -d '{}' https://cc.example.com/api/v1/projects/my-app/deploy

Create or update a project:

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
               "webhook_secret_ref": "vault:my-app-webhook"
             }' \
         https://cc.example.com/api/v1/projects

## Notes

- A deploy returns `202 Accepted` with the new `deployment_id`; poll
  `GET /api/v1/deployments/{id}` for status and the log.
- A project's `source` is `dockerfile`, `compose` or `image`. The `image`
  source needs `image` (and optionally `ports_exposes`/`ports_mappings`); the
  others need `repo_url`.
- A second deploy for the same project while one is running returns `409`.
- A read-only token used on a mutating endpoint returns `403`.
- Missing or invalid tokens return `401`.
- Request and response bodies use the same snake_case field names as
  `servers.yaml` and `projects.yaml`.

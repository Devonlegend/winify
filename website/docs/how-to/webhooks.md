---
title: Webhooks
description: GitHub and GitLab push webhooks, signatures, branch filtering, and replay protection.
---

# Webhooks

```text
POST /webhooks/github/{projectID}
POST /webhooks/gitlab/{projectID}
```

Public, HMAC/token-verified. No session or bearer token required.

## GitHub

- Requires `X-GitHub-Event: push`.
- Verifies `X-Hub-Signature-256` (HMAC-SHA256 of the raw body) against the project's encrypted secret (`webhook_secret_ref`).
- Branch match: if the project has a `branch`, only pushes to it deploy.

## GitLab

- Requires `X-Gitlab-Event: Push Hook`.
- Verifies `X-Gitlab-Token` against the same secret.

## Responses

| Code | Meaning |
| ---- | ------- |
| `202 {"status":"accepted","deployment_id":N}` | Deploy started |
| `202 {"status":"ignored","reason":"..."}` | Non-push, branch mismatch, or duplicate delivery — no deploy |
| `401` | Invalid signature / unknown project (no secret) |
| `409` | Deploy already running for this project |
| `413` | Body over 1 MiB |
| `404` | Unknown project id |

Replay protection: a delivery key derived from the **authenticated body hash** (not the header) is recorded in `webhook_deliveries`; duplicates are ignored. Resending the same `X-GitHub-Delivery` with the same body must not start a second deployment.

## Manual trigger equivalents

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}' \
  http://127.0.0.1:8080/api/v1/projects/my-app/deploy
```

Or the **Deploy** button in the UI (attaches the drawer to the live log; on `409` it points at the in-flight deployment).

## Automatic GitHub registration

Instead of registering hooks manually, configure the GitHub App:

```yaml
github:
  app_id: "123456"
  installation_id: 98765432
  private_key_ref: "vault:github-app-key"
```

The App needs **Administration: read/write** on managed repos and must be installed on them. Store the private key with `control-center cred add github-app-key < app-key.pem`. Then **Projects → `{project}` → Register webhook** (`POST /projects/{id}/webhook/github`) idempotently ensures the push hook and pings a test delivery. Link the project with `github_repo: "owner/repo"` for commit statuses.

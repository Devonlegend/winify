---
title: First deploy
description: Push, webhook, deploy drawer, HTTPS, audit, and rollback in one pass.
---

# First deploy

This walkthrough takes one push all the way to a live HTTPS URL, then rolls it back. It works for both Docker and Windows targets; the pipeline steps differ but the lifecycle is identical.

## 1. Trigger

Three equivalent triggers:

- **Push** to the project's branch (signed webhook).
- **Deploy button** on the Projects tab or resource page.
- **REST API**: `POST /api/v1/projects/{id}/deploy`.

Webhooks:

```text
POST /webhooks/github/{projectID}
POST /webhooks/gitlab/{projectID}
```

- GitHub verifies `X-Hub-Signature-256` (HMAC-SHA256) against the project's encrypted secret and requires `X-GitHub-Event: push`.
- GitLab verifies `X-Gitlab-Token` and requires `X-Gitlab-Event: Push Hook`.
- Responses: `401` bad signature, `202` accepted or ignored (non-push / branch mismatch / duplicate delivery), `409` deploy already running for this project.

## 2. Watch

Open the project → **Deployments** tab. The attempt moves `queued → running → success | failed`. The **deploy drawer** streams the log over SSE (`GET /deployments/{id}/stream`, events `status` / `log`).

What you should see:

- **Docker**: clone → `docker build -t cc/<project>:<sha>` → `docker compose up -d` → health check → proxy register.
- **winsvc**: clone → runtime ensure → optional build → NSSM ensure → validate → stop → timestamped backup → robocopy → install/update service → start → smoke test.
- **IIS**: clone → runtime → optional build → ensure site/app pool → validate `web.config` → stop → backup → copy → start + recycle pool → smoke test.

## 3. Open the URL

On success winify upserts a Caddy route (`host` matcher → `reverse_proxy` to upstream) with a deterministic `@id`. Caddy issues/renews the Let's Encrypt certificate automatically. Open `https://<domain>`.

If DNS does not point at the Caddy host, winify warns after registering — fix the `A`/wildcard record.

## 4. Verify the audit

Every remote command is recorded with server, action, deployment id, redacted command text, and timestamp:

```bash
curl -s -H "Authorization: Bearer $API_TOKEN" \
  http://127.0.0.1:8080/api/v1/audit | head -c 2000
```

Or open the **Audit log** page and filter by server, action, or free text. Secret payloads appear as labels, never values.

## 5. Roll back

Deploy a known-good version, then a bad one. On the Deployment tab click **Roll back**:

- **Dockerfile/image**: re-runs the previous image tag — no rebuild.
- **Compose**: checks out the previous commit and re-runs its compose file.
- **winsvc/IIS**: restores the newest timestamped backup on the target (IIS blue-green repoints to the other slot — effectively instant).
- Every rollback creates a new history entry with `trigger=rollback`.

Re-open the site; the previous version is live.

## Failure is never silent

- Bad signature → `401`, no deployment.
- Wrong branch → `202 reason=branch mismatch`, no deployment.
- Unreachable target → Monitoring card turns red with a `down` pill and the connection error within `monitoring.timeout_seconds`. No stale numbers.
- Failed deploy → status `failed`, error banner above the full log, previous version keeps serving. Docker also dumps `compose ps` + `compose logs` into the deploy log.

Full matrix: [Operations E2E checklist](../operations/e2e-checklist.md).

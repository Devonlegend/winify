---
title: Deploy and roll back
description: Manual, webhook, and API deploys plus rollback artifacts per target.
---

# Deploy and roll back

## Start a deploy

- **Deploy button** (Projects tab / resource page): attaches the SSE drawer to the live log.
- **Push** to `branch`: signed webhook.
- **API**: `POST /api/v1/projects/{id}/deploy` with optional `{"commit":"","ref":""}`. Empty deploys the project's branch. Returns `202 {deployment_id, status: accepted}`; poll `GET /api/v1/deployments/{id}`.

Only one deploy runs per project at a time. A second concurrent trigger gets `409`.

## What success looks like

`queued → running → success`, log showing every stage, artifact recorded (`image_tag` / backup path), Caddy route upserted, notifier fired (`pending` → `success`). Open `https://<domain>`.

## Roll back

**Deployment tab → Roll back** (appears once a prior successful release exists). Creates a new history entry with `trigger=rollback`:

| Target | Rollback artifact |
| ------ | ----------------- |
| Docker `dockerfile` / `image` | Re-run previous image tag, no rebuild |
| Docker `compose` | Check out previous commit, re-run its compose file |
| `winsvc` / `iis` | Restore newest timestamped backup on target, restart service / recycle pool |
| `iis` blue-green | Repoint site to the other slot, no file copy |

On deploy failure winify attempts to restore the previous successful release automatically when one exists; the deploy is still marked `failed` with the full log and error banner.

## Health gate

After starting the app, winify GETs `http://127.0.0.1:<EffectiveHostPort()><health_path>` with retries. Set `disable_health_check: true` for workers/cron. Docker failures include `compose ps` + `compose logs` in the deploy log, read them before re-pushing.

## Timeouts and retention

- Whole-deploy bound: `deploy.timeout_minutes` (default 30). A timed-out deploy is still finalized via a cancellation-proof context.
- History pruned after `deploy.retention_days` (default 90).

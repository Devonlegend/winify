---
title: Notifications
description: Webhook, Slack, and GitHub commit statuses for deploy events. All optional and non-fatal.
---

# Notifications

All sinks are optional and **non-fatal**: fired async, never fail a deploy.

```yaml
notifications:
  webhook_url: ""        # receives DeployEvent JSON on pending/success/failed
  slack_webhook_url: ""  # Slack incoming webhook
```

GitHub commit statuses are posted automatically for projects linked via `github_repo` once the [GitHub App](./github-app.md) is configured — no extra config here.

`DeployEvent` carries project, deployment id, status, commit/ref, domain, and timestamps. Point `webhook_url` at your automation to drive ChatOps or secondary pipelines.

---
title: GitHub App
description: Auto-register push webhooks and post commit statuses with a GitHub App.
---

# GitHub App

Used to auto-register project push webhooks from the UI and to post commit statuses. The App needs **Administration: read/write** on managed repos and must be installed on them.

## Configure

```yaml
github:
  app_id: "123456"
  installation_id: 98765432
  private_key_ref: "vault:github-app-key"
  # api_url: "https://ghe.example.com/api/v3"  # GitHub Enterprise only
```

Store the private key encrypted:

```bash
./control-center cred add github-app-key < app-key.pem
```

## Use

1. Set the project's `github_repo: "owner/repo"`.
2. **Projects → `{project}` → Register webhook** (`POST /projects/{projectID}/webhook/github`).
3. winify mints an RS256 JWT (9-min expiry, 60s clock skew), exchanges it for an installation token, idempotently ensures the push webhook (`EnsurePushHook`), and pings a test delivery.

## Commit statuses

Once linked, deploys post `winify/deploy` statuses (`pending` / `success` / `failure`), only when the project is linked to a repo and the event has a full commit SHA. Failures to post are non-fatal and never fail the deploy.

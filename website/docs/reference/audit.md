---
title: Audit log
description: Every remote command with server, action, deployment, and redacted text.
---

# Audit log

Every remote command — deploy, rollback, monitor, bootstrap, onboard — is recorded in `remote_commands`: **server id, server type, action, deployment id, command, error, timestamp**. Sensitive payloads (env, build args, binary uploads) are replaced by a label via redaction.

Open the **Audit log** page (filter by server, action, time window, free-text search, paginated) or:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "https://cc.example.com/api/v1/audit?limit=100"
```

This is the "what command did the last deploy run?" answer that CI-only tools cannot give. Retention follows `deploy.retention_days` (default 90).

Response headers on all routes include `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, and `Cache-Control: no-store`.

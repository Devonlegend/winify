---
title: Upgrade
description: Replace the binary safely, migrate the database, and verify.
---

# Upgrade

1. **Back up** (database + master key + config) per [Backup and restore](./backup-restore.md).
2. Stop the `winify` service (deploy workers drain up to 30s; in-flight deploys finalize via cancellation-proof context).
3. Replace the binary (`winify.exe` / `control-center`).
4. Start. Embedded SQL migrations run in order on boot; `running` deployments left by the stop are reconciled to `failed` (honest state, not stuck).
5. Verify: `/healthz` → login → **Monitoring** shows samples → one manual **Deploy** on a non-critical project.
6. Check **Setup** page: bootstrap steps should all read `done`; a failed step re-runs without redoing completed ones.

Rollback of winify itself: stop, restore the previous binary **plus** the pre-upgrade database snapshot, start. Do not mix a new database with an old binary.

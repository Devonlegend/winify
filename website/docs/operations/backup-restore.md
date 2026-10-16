---
title: Backup and restore
description: What to copy, how often, and how to restore SQLite plus the master key.
---

# Backup and restore

## What matters

| Artifact | Default | Notes |
| -------- | ------- | ----- |
| SQLite | `data/control-center.db` | Authoritative after first run; inventory, history, sessions, tokens |
| Master key | `data/master.key` (`0600`, symlink-rejected) | Or `CC_MASTER_KEY` / `credentials.master_key` source, without it credentials are unreadable |
| Config | `config.yaml` | Should contain no secrets when `vault:` refs are used |
| IIS/backups on targets | `deploy.iis_backup_dir` | Timestamped rollback copies live on the target, not the controller |

`servers.yaml` / `projects.yaml` are seed-only; do not rely on them as backups once the database is authoritative.

## Procedure

1. Stop winify (or SQLite-backup the live file, never `cp` a live SQLite without a consistent snapshot; prefer `sqlite3 .backup` or stop-then-copy).
2. Copy the database **and** the master key together to versioned storage.
3. Copy `config.yaml`.
4. To restore: stop winify, put both files back with correct permissions (`master.key` `0600`), start, verify `/healthz` and login.

## Retention

- Deployments + audit pruned after `deploy.retention_days` (default 90).
- Metrics pruned after `monitoring.retention_hours` (default 24).

Automated off-site backups (S3 etc.) are V2, for now schedule the stop-snapshot-copy above in your supervisor/cron.

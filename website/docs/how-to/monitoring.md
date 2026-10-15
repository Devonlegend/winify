---
title: Monitor servers
description: Read the Monitoring tab, watch services, and interpret down state.
---

# Monitor servers

Open **Monitoring**: per-server CPU, memory, and disk gauges plus a history chart, pulled over the existing SSH/WinRM connections. No agent.

## What is collected

`cpu`, `mem_total`, `mem_used`, `disk_total`, `disk_used`, `uptime`, `load1`, plus `service:<name>=<state>` per watched service.

Configure per server:

- `services`: systemd units (Linux) or Windows service names to report.
- `disk_path`: volume to report (`/` default Linux, `C:` Windows).

Configure globally:

```yaml
monitoring:
  enabled: true
  interval_seconds: 30
  timeout_seconds: 15
  retention_hours: 24
  history_points: 200
```

## Down means down

An unreachable target records `reachable=0` with the connection error — never stale numbers. The card turns red with a `down` pill within `monitoring.timeout_seconds` and shows the specific error (unreachable host, firewall, bad credential, collection timeout).

If deploys work but monitoring is down, read the card error: same connection, different remote command.

## API

- `GET /api/v1/metrics`
- `GET /api/v1/servers/{id}/metrics?limit=200`

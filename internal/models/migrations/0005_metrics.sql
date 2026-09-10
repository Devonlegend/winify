CREATE TABLE IF NOT EXISTS metrics (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id      TEXT    NOT NULL,
    ts             INTEGER NOT NULL,
    reachable      INTEGER NOT NULL,
    error          TEXT    NOT NULL DEFAULT '',
    cpu_percent    REAL    NOT NULL DEFAULT 0,
    mem_total      INTEGER NOT NULL DEFAULT 0,
    mem_used       INTEGER NOT NULL DEFAULT 0,
    disk_total     INTEGER NOT NULL DEFAULT 0,
    disk_used      INTEGER NOT NULL DEFAULT 0,
    uptime_seconds INTEGER NOT NULL DEFAULT 0,
    load1          REAL    NOT NULL DEFAULT 0,
    services_json  TEXT    NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_metrics_server_ts ON metrics(server_id, ts DESC);

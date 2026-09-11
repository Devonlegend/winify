CREATE TABLE IF NOT EXISTS remote_commands (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id     TEXT    NOT NULL,
    server_type   TEXT    NOT NULL DEFAULT '',
    action        TEXT    NOT NULL DEFAULT '',
    deployment_id INTEGER NOT NULL DEFAULT 0,
    command       TEXT    NOT NULL,
    error         TEXT    NOT NULL DEFAULT '',
    executed_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_remote_commands_server ON remote_commands(server_id, id DESC);

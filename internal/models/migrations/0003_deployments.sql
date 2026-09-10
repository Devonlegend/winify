ALTER TABLE servers ADD COLUMN ssh_port INTEGER NOT NULL DEFAULT 22;

ALTER TABLE projects ADD COLUMN branch TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN domain TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN health_path TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN webhook_secret_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN env_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS deployments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT    NOT NULL,
    commit_sha  TEXT    NOT NULL DEFAULT '',
    ref         TEXT    NOT NULL DEFAULT '',
    image_tag   TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL,
    trigger     TEXT    NOT NULL DEFAULT 'webhook',
    log         TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id, id DESC);

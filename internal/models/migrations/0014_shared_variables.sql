CREATE TABLE IF NOT EXISTS shared_variables (
    scope      TEXT NOT NULL,   -- 'project' (group) or 'environment'
    scope_id   TEXT NOT NULL,   -- group name, or 'group/environment'
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope, scope_id, key)
);

CREATE TABLE IF NOT EXISTS retired_proxy_domains (
    project_id TEXT NOT NULL,
    domain TEXT NOT NULL,
    retired_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, domain)
);
CREATE INDEX IF NOT EXISTS idx_retired_proxy_domains_project ON retired_proxy_domains(project_id);

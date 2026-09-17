ALTER TABLE servers ADD COLUMN public_ip TEXT NOT NULL DEFAULT '';

ALTER TABLE projects ADD COLUMN ports_exposes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN ports_mappings TEXT NOT NULL DEFAULT '[]';
ALTER TABLE projects ADD COLUMN build_env_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE projects ADD COLUMN health_interval_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN health_timeout_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN health_retries INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN health_start_period_seconds INTEGER NOT NULL DEFAULT 0;

-- Backfill: the old host port becomes a "host:container" mapping and the old
-- container port becomes ports_exposes. Existing single-port projects get a
-- 1:1 publish so the central proxy can still reach them.
UPDATE projects
SET ports_exposes = CASE WHEN container_port > 0 THEN container_port ELSE port END
WHERE ports_exposes = 0 AND port > 0;

UPDATE projects
SET ports_mappings = '["' || port || ':' || (CASE WHEN container_port > 0 THEN container_port ELSE port END) || '"]'
WHERE ports_mappings = '[]' AND port > 0;

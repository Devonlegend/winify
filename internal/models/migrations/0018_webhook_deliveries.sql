CREATE TABLE IF NOT EXISTS webhook_deliveries (
    provider TEXT NOT NULL,
    project_id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL DEFAULT '',
    received_at INTEGER NOT NULL,
    PRIMARY KEY (provider, project_id, delivery_id)
);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_received_at ON webhook_deliveries(received_at);

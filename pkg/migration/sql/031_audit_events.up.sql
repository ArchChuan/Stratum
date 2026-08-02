CREATE TABLE IF NOT EXISTS audit_events (
    id          uuid PRIMARY KEY,
    tenant_id   text NOT NULL,
    actor_id    text NOT NULL,
    actor_type  text NOT NULL DEFAULT 'system',
    action      text NOT NULL,
    resource_type text NOT NULL DEFAULT '',
    resource_id   text NOT NULL DEFAULT '',
    before      jsonb,
    after       jsonb,
    request_id  text NOT NULL DEFAULT '',
    trace_id    text NOT NULL DEFAULT '',
    risk_level  text NOT NULL DEFAULT 'low',
    outcome     text NOT NULL DEFAULT 'success',
    occurred_at timestamptz NOT NULL DEFAULT now(),
    previous_hash text
);

CREATE INDEX IF NOT EXISTS idx_audit_events_tenant ON audit_events (tenant_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_resource ON audit_events (resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events (action, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_occurred ON audit_events (occurred_at);

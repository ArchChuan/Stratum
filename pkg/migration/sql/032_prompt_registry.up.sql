CREATE TABLE IF NOT EXISTS prompt_templates (
    id          SERIAL PRIMARY KEY,
    key         TEXT NOT NULL,
    tenant_id   TEXT,  -- NULL = global
    version     INT NOT NULL,
    content     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',  -- draft|published|archived
    content_hash TEXT NOT NULL,  -- SHA-256 hex
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (key, tenant_id, version)
);

CREATE INDEX IF NOT EXISTS idx_prompt_templates_key_status
    ON prompt_templates (key, tenant_id, status);

CREATE INDEX IF NOT EXISTS idx_prompt_templates_hash
    ON prompt_templates (content_hash);

CREATE TABLE IF NOT EXISTS prompt_bindings (
    id                SERIAL PRIMARY KEY,
    key               TEXT NOT NULL,
    scope             TEXT NOT NULL,  -- tenant:<id>|agent:<id>
    stable_version_id TEXT NOT NULL,
    canary_version_id TEXT NOT NULL DEFAULT '',
    traffic_percent   INT NOT NULL DEFAULT 0,
    UNIQUE (key, scope)
);

CREATE INDEX IF NOT EXISTS idx_prompt_bindings_scope
    ON prompt_bindings (scope);

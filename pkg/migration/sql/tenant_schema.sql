-- Per-tenant schema DDL
-- Execute after: SET search_path = tenant_{id}, public

CREATE TABLE IF NOT EXISTS agents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    description    TEXT,
    config         JSONB NOT NULL DEFAULT '{}',
    allowed_skills TEXT[] NOT NULL DEFAULT '{}',
    max_context_tokens INTEGER NOT NULL DEFAULT 8000,
    memory_enabled BOOLEAN NOT NULL DEFAULT true,
    memory_scope   VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_scope VARCHAR(20) NOT NULL DEFAULT 'user';

CREATE TABLE IF NOT EXISTS skills (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mcp_configs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    server_id    TEXT NOT NULL,
    transport    TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE SET NULL,
    user_id      TEXT NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID REFERENCES sessions(id) ON DELETE CASCADE,
    user_id      TEXT,
    agent_id     TEXT REFERENCES agents(id) ON DELETE SET NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    type         TEXT NOT NULL DEFAULT 'short_term',
    importance   FLOAT8 NOT NULL DEFAULT 0,
    tags         TEXT[] NOT NULL DEFAULT '{}',
    metadata     JSONB NOT NULL DEFAULT '{}',
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- idempotent backfill: existing tenants provisioned before user_id/agent_id were added
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS entities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS entity_relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_id      UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    to_id        UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    relation     TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS knowledge_docs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    source       TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS exec_history (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE SET NULL,
    session_id   UUID REFERENCES sessions(id) ON DELETE SET NULL,
    skill_id     UUID REFERENCES skills(id) ON DELETE SET NULL,
    input        JSONB NOT NULL DEFAULT '{}',
    output       JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS llm_api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider     TEXT NOT NULL,
    key_hint     TEXT NOT NULL,
    encrypted    TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS model_presets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    model_id     TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS model_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_preset_id UUID REFERENCES model_presets(id) ON DELETE SET NULL,
    input_tokens    INT NOT NULL DEFAULT 0,
    output_tokens   INT NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12,6) NOT NULL DEFAULT 0,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS model_quotas (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_preset_id UUID REFERENCES model_presets(id) ON DELETE CASCADE,
    period          TEXT NOT NULL DEFAULT 'monthly',
    max_tokens      BIGINT NOT NULL,
    max_cost_usd    NUMERIC(12,2),
    reset_at        TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_templates (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    template     TEXT NOT NULL,
    variables    TEXT[] NOT NULL DEFAULT '{}',
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workflows (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    definition   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id  UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    input        JSONB NOT NULL DEFAULT '{}',
    output       JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    cron_expr    TEXT NOT NULL,
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    payload      JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- idempotent backfill: existing tenants provisioned before allowed_skills was added
ALTER TABLE agents ADD COLUMN IF NOT EXISTS allowed_skills TEXT[] NOT NULL DEFAULT '{}';
-- idempotent backfill: existing tenants provisioned before max_context_tokens was added
ALTER TABLE agents ADD COLUMN IF NOT EXISTS max_context_tokens INTEGER NOT NULL DEFAULT 8000;

CREATE TABLE IF NOT EXISTS webhooks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url          TEXT NOT NULL,
    events       TEXT[] NOT NULL DEFAULT '{}',
    secret_hint  TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id   UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    status_code  INT,
    response     TEXT,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chat_conversations (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id   UUID        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    name       TEXT        NOT NULL DEFAULT '新会话',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days'
);
CREATE INDEX IF NOT EXISTS idx_chat_conv_agent_user
    ON chat_conversations (agent_id, user_id, expires_at DESC);

CREATE TABLE IF NOT EXISTS chat_messages (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID        NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    role            TEXT        NOT NULL CHECK (role IN ('user', 'agent')),
    content         TEXT        NOT NULL,
    steps_json      JSONB       NOT NULL DEFAULT '[]',
    is_error        BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_chat_msg_conv
    ON chat_messages (conversation_id, created_at ASC);

CREATE TABLE IF NOT EXISTS rag_workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_workspaces (
    agent_id     UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES rag_workspaces(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, workspace_id)
);

CREATE TABLE IF NOT EXISTS agent_executions (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id       TEXT        NOT NULL,
    agent_name     TEXT        NOT NULL DEFAULT '',
    user_id        TEXT        NOT NULL,
    status         TEXT        NOT NULL CHECK (status IN ('success', 'error')),
    input_preview  TEXT        NOT NULL DEFAULT '',
    output_preview TEXT        NOT NULL DEFAULT '',
    error_message  TEXT        NOT NULL DEFAULT '',
    total_tokens   INT         NOT NULL DEFAULT 0,
    duration_ms    INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agent_exec_created
    ON agent_executions (created_at DESC);

-- Memory pipeline tables
CREATE TABLE IF NOT EXISTS memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_outbox_created ON memory_outbox (created_at);

CREATE TABLE IF NOT EXISTS memory_summaries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    summary         TEXT NOT NULL,
    covered_until   TIMESTAMPTZ NOT NULL,
    token_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_summaries_conv ON memory_summaries (conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_token_budgets (
    conversation_id UUID PRIMARY KEY REFERENCES chat_conversations(id) ON DELETE CASCADE,
    accumulated     INT NOT NULL DEFAULT 0,
    last_reset_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS keywords TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS token_estimate INT NOT NULL DEFAULT 0;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS scope_layer;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS enriched_at TIMESTAMPTZ;

ALTER TABLE entities ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS confidence FLOAT8 NOT NULL DEFAULT 0;
ALTER TABLE entities DROP COLUMN IF EXISTS scope_layer;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS occurrence_count INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_entities_user ON entities (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type ON entities (user_id, COALESCE(agent_id, ''), name, type);
CREATE INDEX IF NOT EXISTS idx_memory_entries_user_id ON memory_entries (user_id);

CREATE INDEX IF NOT EXISTS idx_memory_entries_content_trgm ON memory_entries USING GIN (content gin_trgm_ops);

-- idempotent backfill: embed_model for tenant-level inheritance
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';

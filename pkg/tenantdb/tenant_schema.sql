-- Per-tenant schema DDL
-- Execute after: SET search_path = tenant_{id}, public

CREATE TABLE IF NOT EXISTS agents (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    type           TEXT NOT NULL DEFAULT 'react',
    description    TEXT NOT NULL DEFAULT '',
    persona        TEXT NOT NULL DEFAULT '',
    system_prompt  TEXT NOT NULL DEFAULT '',
    llm_model      TEXT NOT NULL DEFAULT '',
    embed_model    TEXT NOT NULL DEFAULT '',
    max_iterations INT  NOT NULL DEFAULT 10,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS skills (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mcp_configs (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL DEFAULT '' UNIQUE,
    transport     TEXT NOT NULL,
    command       TEXT NOT NULL DEFAULT '',
    url           TEXT NOT NULL DEFAULT '',
    args          JSONB NOT NULL DEFAULT '[]',
    env           JSONB NOT NULL DEFAULT '{}',
    capabilities  JSONB NOT NULL DEFAULT '[]',
    timeout_sec   INT  NOT NULL DEFAULT 30,
    enabled       BOOL NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_mcp_links (
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    server_id  TEXT NOT NULL REFERENCES mcp_configs(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, server_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     TEXT REFERENCES agents(id) ON DELETE SET NULL,
    user_id      TEXT NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
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
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}',
    confidence   FLOAT NOT NULL DEFAULT 0.5,
    user_id      TEXT,
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- idempotent backfill: existing tenants provisioned before user_id was added
ALTER TABLE entities ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type_user
    ON entities (name, type, user_id);

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
    workspace_id UUID,
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    source       TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS exec_history (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     TEXT REFERENCES agents(id) ON DELETE SET NULL,
    session_id   UUID REFERENCES sessions(id) ON DELETE SET NULL,
    skill_id     TEXT REFERENCES skills(id) ON DELETE SET NULL,
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
    agent_id     TEXT REFERENCES agents(id) ON DELETE CASCADE,
    payload      JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS rag_workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chat_conversations (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id   TEXT        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    name       TEXT        NOT NULL DEFAULT '新会话',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days',
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_chat_conv_agent_user
    ON chat_conversations (agent_id, user_id, expires_at DESC);

CREATE TABLE IF NOT EXISTS chat_messages (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    conversation_id UUID        NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    role            TEXT        NOT NULL CHECK (role IN ('user', 'agent')),
    content         TEXT        NOT NULL,
    steps_json      JSONB       NOT NULL DEFAULT '[]',
    is_error        BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_chat_msg_conv
    ON chat_messages (conversation_id, created_at ASC);

CREATE TABLE IF NOT EXISTS agent_workspaces (
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES rag_workspaces(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, workspace_id)
);

CREATE TABLE IF NOT EXISTS agent_skill_links (
    agent_id  TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_id  TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, skill_id)
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

-- =============================================================================
-- Memory Pipeline tables (async outbox → embedder → enricher)
-- =============================================================================

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

-- memory_entries extensions for pipeline
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS keywords TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS token_estimate INT NOT NULL DEFAULT 0;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS enriched_at TIMESTAMPTZ;

-- entities extensions for pipeline
ALTER TABLE entities ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS occurrence_count INT NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_entities_scope ON entities (user_id, agent_id, scope_layer);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type ON entities (user_id, COALESCE(agent_id, ''), name, type);

-- agents extensions
ALTER TABLE agents ADD COLUMN IF NOT EXISTS max_context_tokens INTEGER NOT NULL DEFAULT 8000;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';

-- chat_conversations soft-delete backfill
ALTER TABLE chat_conversations ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_chat_conversations_deleted ON chat_conversations (deleted_at) WHERE deleted_at IS NOT NULL;

-- uuid v7 default backfill: switch existing tenant tables from gen_random_uuid() to public.gen_uuid_v7()
ALTER TABLE memory_entries ALTER COLUMN id SET DEFAULT public.gen_uuid_v7();
ALTER TABLE entities       ALTER COLUMN id SET DEFAULT public.gen_uuid_v7();
ALTER TABLE chat_messages  ALTER COLUMN id SET DEFAULT public.gen_uuid_v7();

CREATE INDEX IF NOT EXISTS idx_memory_entries_content_trgm ON memory_entries USING GIN (content gin_trgm_ops);

-- cascade delete backfill: fix RESTRICT → CASCADE on relationship tables
-- idempotent: DROP IF EXISTS then re-add with CASCADE
ALTER TABLE agent_mcp_links DROP CONSTRAINT IF EXISTS agent_mcp_links_server_id_fkey;
ALTER TABLE agent_mcp_links ADD CONSTRAINT agent_mcp_links_server_id_fkey
    FOREIGN KEY (server_id) REFERENCES mcp_configs(id) ON DELETE CASCADE;

ALTER TABLE agent_skill_links DROP CONSTRAINT IF EXISTS agent_skill_links_skill_id_fkey;
ALTER TABLE agent_skill_links ADD CONSTRAINT agent_skill_links_skill_id_fkey
    FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE;

ALTER TABLE agent_workspaces DROP CONSTRAINT IF EXISTS agent_workspaces_workspace_id_fkey;
ALTER TABLE agent_workspaces ADD CONSTRAINT agent_workspaces_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES rag_workspaces(id) ON DELETE CASCADE;

-- knowledge_docs: add workspace FK (nullable, existing docs have no workspace)
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS workspace_id UUID;
ALTER TABLE knowledge_docs DROP CONSTRAINT IF EXISTS knowledge_docs_workspace_id_fkey;
ALTER TABLE knowledge_docs ADD CONSTRAINT knowledge_docs_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES rag_workspaces(id) ON DELETE CASCADE;

-- mcp_configs: add auth, retry, headers, version columns (idempotent backfill)
ALTER TABLE mcp_configs ADD COLUMN IF NOT EXISTS version TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_configs ADD COLUMN IF NOT EXISTS headers JSONB NOT NULL DEFAULT '{}';
ALTER TABLE mcp_configs ADD COLUMN IF NOT EXISTS auth_config JSONB NOT NULL DEFAULT '{}';
ALTER TABLE mcp_configs ADD COLUMN IF NOT EXISTS retry_config JSONB NOT NULL DEFAULT '{}';

-- Per-tenant schema DDL
-- Execute after: SET search_path = tenant_{id}, public

-- Drop obsolete tables (idempotent; runs on every startup via ProvisionAllTenantSchemas)
ALTER TABLE IF EXISTS memory_entries DROP COLUMN IF EXISTS session_id;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;
DROP TABLE IF EXISTS model_quotas;
DROP TABLE IF EXISTS model_usage;
DROP TABLE IF EXISTS model_presets;
DROP TABLE IF EXISTS scheduled_tasks;
DROP TABLE IF EXISTS prompt_templates;
DROP TABLE IF EXISTS exec_history;
DROP TABLE IF EXISTS entity_relations;
DROP TABLE IF EXISTS memory_token_budgets;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS entities;
DROP TABLE IF EXISTS llm_api_keys;

CREATE TABLE IF NOT EXISTS agents (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    type           TEXT NOT NULL DEFAULT 'react',
    description    TEXT NOT NULL DEFAULT '',
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
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
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

CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
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

CREATE TABLE IF NOT EXISTS knowledge_docs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID,
    title        TEXT NOT NULL,
    source       TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'user';

-- memory_entries extensions for pipeline
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS keywords TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS token_estimate INT NOT NULL DEFAULT 0;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS scope_layer;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS enriched_at TIMESTAMPTZ;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'user';
UPDATE memory_entries SET scope = 'agent' WHERE agent_id IS NOT NULL AND scope = 'user';

CREATE INDEX IF NOT EXISTS idx_memory_entries_user_id ON memory_entries (user_id);

-- agents extensions
ALTER TABLE agents ADD COLUMN IF NOT EXISTS max_context_tokens INTEGER NOT NULL DEFAULT 8000;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';
ALTER TABLE agents DROP COLUMN IF EXISTS persona;

-- chat_conversations soft-delete backfill
ALTER TABLE chat_conversations ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_chat_conversations_deleted ON chat_conversations (deleted_at) WHERE deleted_at IS NOT NULL;

-- uuid v7 default backfill: switch existing tenant tables from gen_random_uuid() to public.gen_uuid_v7()
ALTER TABLE memory_entries ALTER COLUMN id SET DEFAULT public.gen_uuid_v7();
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

-- chat_messages: rename role 'agent' → 'assistant' (LLM protocol alignment).
-- Idempotent: drop old check, backfill rows, re-add with new constraint.
ALTER TABLE chat_messages DROP CONSTRAINT IF EXISTS chat_messages_role_check;
UPDATE chat_messages SET role = 'assistant' WHERE role = 'agent';
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_role_check
    CHECK (role IN ('user', 'assistant'));

-- =============================================================================
-- Memory v2 Tables (fact-centric model)
-- =============================================================================

-- memory_facts: core fact storage with frecency scoring
CREATE TABLE IF NOT EXISTS memory_facts (
    id              UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id         TEXT NOT NULL,
    agent_id        TEXT,
    scope           TEXT NOT NULL CHECK (scope IN ('user', 'agent')),
    content         TEXT NOT NULL,
    importance      FLOAT8 NOT NULL DEFAULT 0.5 CHECK (importance BETWEEN 0 AND 1),
    frecency_score  FLOAT8 NOT NULL DEFAULT 0,
    access_count    INT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    superseded_by   UUID REFERENCES memory_facts(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'archived')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_facts_user_scope ON memory_facts (user_id, scope, status);
CREATE INDEX IF NOT EXISTS idx_memory_facts_frecency ON memory_facts (frecency_score DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_memory_facts_content_trgm ON memory_facts USING GIN (content gin_trgm_ops);
ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
-- Enforce only one active fact can supersede another (prevent supersede loops)
CREATE UNIQUE INDEX IF NOT EXISTS memory_facts_one_active_supersede
    ON memory_facts (superseded_by)
    WHERE status = 'active';

-- memory_entities: recognized entities with rolling profiles
CREATE TABLE IF NOT EXISTS memory_entities (
    id                      UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id                 TEXT NOT NULL,
    agent_id                TEXT,
    scope                   TEXT NOT NULL CHECK (scope IN ('user', 'agent')),
    name                    TEXT NOT NULL,
    entity_type             TEXT NOT NULL,
    profile                 TEXT NOT NULL DEFAULT '',
    fact_count              INT NOT NULL DEFAULT 0,
    fact_count_since_rebuild INT NOT NULL DEFAULT 0,
    last_seen_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_profile_rebuild_at TIMESTAMPTZ,
    status                  TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_entities_user_scope ON memory_entities (user_id, scope, status);
ALTER TABLE memory_entities ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_memory_entities_name_trgm ON memory_entities USING GIN (name gin_trgm_ops);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_entities_name_type_scope
    ON memory_entities (name, entity_type, user_id, COALESCE(agent_id, ''));
-- backfill: entity_repo.go uses rebuild_after; older schema had last_profile_rebuild_at only
ALTER TABLE memory_entities ADD COLUMN IF NOT EXISTS rebuild_after TIMESTAMPTZ;

-- extraction_queue: async LLM extraction tasks
CREATE TABLE IF NOT EXISTS memory_extraction_queue (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    agent_id    TEXT,
    content     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    retry_count INT NOT NULL DEFAULT 0,
    error_msg   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_extraction_queue_status ON memory_extraction_queue (status, created_at);
ALTER TABLE memory_extraction_queue ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_extraction_queue ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'user';

-- agents extensions for memory v2 config
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_write_scope TEXT NOT NULL DEFAULT 'user' CHECK (memory_write_scope IN ('user', 'agent'));
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_read_scope TEXT NOT NULL DEFAULT 'user' CHECK (memory_read_scope IN ('user', 'agent'));
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_scope TEXT NOT NULL DEFAULT 'agent';

-- backfill agents created before max_iterations/max_context_tokens were wired in the form
UPDATE agents SET max_iterations = 10 WHERE max_iterations = 0;
UPDATE agents SET max_context_tokens = 8000 WHERE max_context_tokens = 0;

-- backfill timestamp columns added to existing tables
ALTER TABLE skills ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- dedup: content hash for knowledge_docs
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS content_hash TEXT;
CREATE INDEX IF NOT EXISTS idx_knowledge_docs_ws_hash ON knowledge_docs (workspace_id, content_hash);

-- knowledge_chunks: full-text search index for keyword/hybrid RAG
CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id             TEXT PRIMARY KEY,
    workspace_id   UUID NOT NULL REFERENCES rag_workspaces(id) ON DELETE CASCADE,
    doc_id         TEXT NOT NULL,
    chunk_index    BIGINT NOT NULL,
    content        TEXT NOT NULL,
    tsv            tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kc_tsv       ON knowledge_chunks USING GIN(tsv);
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS workspace_id UUID;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'knowledge_chunks'
          AND column_name = 'workspace_name'
    ) THEN
        UPDATE knowledge_chunks kc
        SET workspace_id = rw.id
        FROM rag_workspaces rw
        WHERE kc.workspace_id IS NULL
          AND kc.workspace_name = rw.name;
    END IF;
END $$;
DELETE FROM knowledge_chunks WHERE workspace_id IS NULL;
ALTER TABLE knowledge_chunks ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_workspace_id_fkey;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES rag_workspaces(id) ON DELETE CASCADE;
DROP INDEX IF EXISTS idx_kc_workspace;
ALTER TABLE knowledge_chunks DROP COLUMN IF EXISTS workspace_name;
CREATE INDEX IF NOT EXISTS idx_kc_workspace ON knowledge_chunks(workspace_id);

-- drop obsolete content column from knowledge_docs (content stored in chunks)
ALTER TABLE knowledge_docs DROP COLUMN IF EXISTS content;

-- async ingest lifecycle: track status/progress of embedding jobs that run
-- in a detached background goroutine after the API returns 202
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS ingest_status TEXT NOT NULL DEFAULT 'completed'
    CHECK (ingest_status IN ('processing', 'completed', 'failed'));
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS ingest_error TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS processed_chunks INT NOT NULL DEFAULT 0;
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS total_chunks INT NOT NULL DEFAULT 0;
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS ingest_started_at TIMESTAMPTZ;
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS ingest_finished_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_knowledge_docs_ws_status ON knowledge_docs (workspace_id, ingest_status);

-- Parent chunks: large context units for Parent-Child chunking strategies.
-- Leaves reference these via knowledge_chunks.parent_id.
CREATE TABLE IF NOT EXISTS knowledge_parent_chunks (
    id           TEXT PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES rag_workspaces(id) ON DELETE CASCADE,
    doc_id       TEXT NOT NULL,
    chunk_index  BIGINT NOT NULL,
    content      TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kpc_workspace ON knowledge_parent_chunks(workspace_id);
CREATE INDEX IF NOT EXISTS idx_kpc_doc       ON knowledge_parent_chunks(workspace_id, doc_id);

-- Add parent_id to leaf chunks (NULL for strategies without Parent-Child).
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS parent_id TEXT
    REFERENCES knowledge_parent_chunks(id) ON DELETE SET NULL;

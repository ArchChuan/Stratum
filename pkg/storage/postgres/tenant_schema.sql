-- Per-tenant schema DDL
-- Execute after: SET search_path = tenant_{id}, public

-- Drop obsolete tables (idempotent; runs on every startup via ProvisionAllTenantSchemas)
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
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
DROP TABLE IF EXISTS agent_mcp_links;

CREATE TABLE IF NOT EXISTS agents (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    type           TEXT NOT NULL DEFAULT 'react',
    description    TEXT NOT NULL DEFAULT '',
    system_prompt  TEXT NOT NULL DEFAULT '',
    llm_model      TEXT NOT NULL DEFAULT '',
    embed_model    TEXT NOT NULL DEFAULT '',
    max_iterations INT  NOT NULL DEFAULT 10,
    max_context_tokens INTEGER NOT NULL DEFAULT 8000,
    memory_scope   TEXT NOT NULL DEFAULT 'agent',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE agents ADD COLUMN IF NOT EXISTS max_context_tokens INTEGER NOT NULL DEFAULT 8000;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_scope TEXT NOT NULL DEFAULT 'agent';

-- Executable Skills were replaced by versioned instruction bundles. Detect
-- the legacy shape instead of using a permanent DROP so repeated tenant
-- provisioning never deletes data written to the new tables.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'skill_versions'
          AND column_name = 'implementation'
    ) THEN
        IF to_regclass('evaluation_deployments') IS NOT NULL THEN
            DELETE FROM evaluation_deployments WHERE resource_kind = 'skill';
        END IF;
        IF to_regclass('evaluation_experiments') IS NOT NULL THEN
            DELETE FROM evaluation_experiments WHERE resource_kind = 'skill';
        END IF;
        IF to_regclass('optimization_jobs') IS NOT NULL THEN
            DELETE FROM optimization_jobs WHERE resource_kind = 'skill';
        END IF;
        IF to_regclass('eval_runs') IS NOT NULL THEN
            DELETE FROM eval_runs WHERE resource_kind = 'skill';
        END IF;
        IF to_regclass('evaluation_feedback') IS NOT NULL THEN
            DELETE FROM evaluation_feedback WHERE resource_kind = 'skill';
        END IF;
        IF to_regclass('evaluation_jobs') IS NOT NULL THEN
            DELETE FROM evaluation_jobs WHERE payload->>'resource_kind' = 'skill';
        END IF;
        IF to_regclass('eval_suite_revisions') IS NOT NULL THEN
            DELETE FROM eval_suite_revisions WHERE resource_kind = 'skill';
            DELETE FROM eval_suites s
            WHERE NOT EXISTS (SELECT 1 FROM eval_suite_revisions r WHERE r.suite_id = s.id);
        END IF;

        DROP TABLE IF EXISTS agent_skill_links;
        DROP TABLE IF EXISTS skill_eval_runs;
        DROP TABLE IF EXISTS skill_test_cases;
        DROP TABLE IF EXISTS skill_versions;
        DROP TABLE IF EXISTS skills;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS skills (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'draft',
    active_revision_id TEXT,
    draft_revision_id  TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS skill_revisions (
    id                  TEXT PRIMARY KEY,
    skill_id            TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    parent_revision_id  TEXT REFERENCES skill_revisions(id) ON DELETE SET NULL,
    revision_no         INT,
    status              TEXT NOT NULL DEFAULT 'draft',
    source              TEXT NOT NULL DEFAULT 'manual',
    content_hash        TEXT NOT NULL DEFAULT '',
    generation_metadata JSONB NOT NULL DEFAULT '{}',
    capability          JSONB NOT NULL DEFAULT '{}',
    activation_contract JSONB NOT NULL DEFAULT '{}',
    instructions        TEXT NOT NULL DEFAULT '',
    requirements        JSONB NOT NULL DEFAULT '{}',
    publish_checks      JSONB NOT NULL DEFAULT '{}',
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_revisions_one_draft
    ON skill_revisions(skill_id)
    WHERE status = 'draft';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_revisions_published_no
    ON skill_revisions(skill_id, revision_no)
    WHERE revision_no IS NOT NULL;

-- Generic evaluation and optimization control plane. Resource payloads remain
-- owned by their bounded context; these tables store immutable references and evidence.
CREATE TABLE IF NOT EXISTS eval_suites (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    active_revision_id TEXT,
    draft_revision_id  TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS eval_suite_revisions (
    id            TEXT PRIMARY KEY,
    suite_id      TEXT NOT NULL REFERENCES eval_suites(id) ON DELETE CASCADE,
    parent_id     TEXT REFERENCES eval_suite_revisions(id) ON DELETE SET NULL,
    version_no    INT,
    status        TEXT NOT NULL DEFAULT 'draft',
    resource_kind TEXT NOT NULL,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_eval_suite_revision_no
    ON eval_suite_revisions(suite_id, version_no) WHERE version_no IS NOT NULL;

CREATE TABLE IF NOT EXISTS eval_cases (
    id                TEXT PRIMARY KEY,
    suite_revision_id TEXT NOT NULL REFERENCES eval_suite_revisions(id) ON DELETE CASCADE,
    name              TEXT NOT NULL DEFAULT '',
    input             JSONB NOT NULL DEFAULT '{}',
    expected_output   JSONB NOT NULL DEFAULT '{}',
    assertion_mode    TEXT NOT NULL DEFAULT 'contains',
    evaluator_config  JSONB NOT NULL DEFAULT '{}',
    tags              TEXT[] NOT NULL DEFAULT '{}',
    critical          BOOL NOT NULL DEFAULT false,
    enabled           BOOL NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_cases_revision ON eval_cases(suite_revision_id);

CREATE TABLE IF NOT EXISTS eval_runs (
    id                TEXT PRIMARY KEY,
    resource_kind     TEXT NOT NULL,
    resource_id       TEXT NOT NULL,
    revision_id       TEXT NOT NULL,
    suite_revision_id TEXT NOT NULL REFERENCES eval_suite_revisions(id) ON DELETE RESTRICT,
    status            TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    passed            BOOL NOT NULL DEFAULT false,
    total_cases       INT NOT NULL DEFAULT 0,
    passed_cases      INT NOT NULL DEFAULT 0,
    metrics           JSONB NOT NULL DEFAULT '{}',
    error_message     TEXT NOT NULL DEFAULT '',
    idempotency_key   TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_eval_runs_resource
    ON eval_runs(resource_kind, resource_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_eval_runs_idempotency
    ON eval_runs(idempotency_key) WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS eval_case_results (
    id              TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    case_id         TEXT REFERENCES eval_cases(id) ON DELETE SET NULL,
    passed          BOOL NOT NULL DEFAULT false,
    actual_output   JSONB NOT NULL DEFAULT 'null',
    message         TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    trace_id        TEXT NOT NULL DEFAULT '',
    tokens          INT NOT NULL DEFAULT 0,
    cost_usd        DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_ms     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_case_results_run ON eval_case_results(run_id);

CREATE TABLE IF NOT EXISTS optimization_jobs (
    id                    TEXT PRIMARY KEY,
    resource_kind         TEXT NOT NULL,
    resource_id           TEXT NOT NULL,
    baseline_revision_id  TEXT NOT NULL,
    suite_revision_id     TEXT NOT NULL REFERENCES eval_suite_revisions(id) ON DELETE RESTRICT,
    status                TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    search_space          JSONB NOT NULL DEFAULT '{}',
    rewrite_config        JSONB NOT NULL DEFAULT '{}',
    error_message         TEXT NOT NULL DEFAULT '',
    created_by            TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at          TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS optimization_candidates (
    id                    TEXT PRIMARY KEY,
    optimization_job_id   TEXT NOT NULL REFERENCES optimization_jobs(id) ON DELETE CASCADE,
    revision_id           TEXT NOT NULL,
    parent_revision_id    TEXT NOT NULL,
    source                TEXT NOT NULL,
    rationale             TEXT NOT NULL DEFAULT '',
    generation_metadata   JSONB NOT NULL DEFAULT '{}',
    eval_run_id           TEXT REFERENCES eval_runs(id) ON DELETE SET NULL,
    rank                  INT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS evaluation_experiments (
    id                    TEXT PRIMARY KEY,
    resource_kind         TEXT NOT NULL,
    resource_id           TEXT NOT NULL,
    stable_revision_id    TEXT NOT NULL,
    canary_revision_id    TEXT NOT NULL,
    suite_revision_id     TEXT NOT NULL REFERENCES eval_suite_revisions(id) ON DELETE RESTRICT,
    status                TEXT NOT NULL,
    stage_percent         INT NOT NULL DEFAULT 5,
    policy                JSONB NOT NULL DEFAULT '{}',
    decision_snapshot     JSONB NOT NULL DEFAULT '{}',
    created_by            TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at          TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_evaluation_experiments_resource
    ON evaluation_experiments(resource_kind, resource_id, created_at DESC);

CREATE TABLE IF NOT EXISTS evaluation_deployments (
    resource_kind      TEXT NOT NULL,
    resource_id        TEXT NOT NULL,
    stable_revision_id TEXT NOT NULL,
    canary_revision_id TEXT,
    canary_percent     INT NOT NULL DEFAULT 0 CHECK (canary_percent BETWEEN 0 AND 100),
    experiment_id      TEXT REFERENCES evaluation_experiments(id) ON DELETE SET NULL,
    policy_version     INT NOT NULL DEFAULT 1,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS evaluation_feedback (
    id              TEXT PRIMARY KEY,
    trace_id        TEXT NOT NULL,
    resource_kind   TEXT NOT NULL,
    resource_id     TEXT NOT NULL,
    revision_id     TEXT NOT NULL,
    score           DOUBLE PRECISION,
    outcome         JSONB NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (idempotency_key),
    UNIQUE (trace_id, resource_id)
);
CREATE INDEX IF NOT EXISTS idx_evaluation_feedback_resource
    ON evaluation_feedback(resource_kind, resource_id, revision_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_evaluation_feedback_trace_resource
    ON evaluation_feedback(trace_id, resource_id);

CREATE TABLE IF NOT EXISTS evaluation_jobs (
    id              TEXT PRIMARY KEY,
    job_type        TEXT NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    attempts        INT NOT NULL DEFAULT 0,
    lease_owner     TEXT NOT NULL DEFAULT '',
    lease_until     TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    result_id       TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (idempotency_key)
);
ALTER TABLE evaluation_jobs ADD COLUMN IF NOT EXISTS result_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_evaluation_jobs_claim
    ON evaluation_jobs(status, lease_until, created_at);

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

CREATE TABLE IF NOT EXISTS agent_mcp_tool_links (
    agent_id  TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    server_id TEXT NOT NULL REFERENCES mcp_configs(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    PRIMARY KEY (agent_id, server_id, tool_name)
);

-- Tool risk is tenant-owned policy. MCP servers may describe tools but cannot
-- assign themselves a trusted risk level.
CREATE TABLE IF NOT EXISTS mcp_tool_policies (
    server_id   TEXT NOT NULL REFERENCES mcp_configs(id) ON DELETE CASCADE,
    tool_name   TEXT NOT NULL,
    risk_level TEXT NOT NULL DEFAULT 'unclassified'
        CHECK (risk_level IN ('read', 'write_reversible', 'destructive', 'unclassified')),
    updated_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (server_id, tool_name)
);

CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id      TEXT,
    session_id   TEXT,
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
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS session_id TEXT;

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
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_id   TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    revision_id TEXT REFERENCES skill_revisions(id) ON DELETE SET NULL,
    PRIMARY KEY (agent_id, skill_id)
);

CREATE TABLE IF NOT EXISTS agent_executions (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id       TEXT        NOT NULL DEFAULT '',
    agent_id       TEXT        NOT NULL,
    agent_name     TEXT        NOT NULL DEFAULT '',
    user_id        TEXT        NOT NULL,
    status         TEXT        NOT NULL CHECK (status IN ('success', 'error', 'waiting_approval')),
    input_preview  TEXT        NOT NULL DEFAULT '',
    output_preview TEXT        NOT NULL DEFAULT '',
    error_message  TEXT        NOT NULL DEFAULT '',
    total_tokens   INT         NOT NULL DEFAULT 0,
    duration_ms    INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_executions DROP CONSTRAINT IF EXISTS agent_executions_status_check;
ALTER TABLE agent_executions ADD CONSTRAINT agent_executions_status_check
    CHECK (status IN ('success', 'error', 'waiting_approval'));
CREATE INDEX IF NOT EXISTS idx_agent_exec_created
    ON agent_executions (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_exec_trace
    ON agent_executions (trace_id);

CREATE TABLE IF NOT EXISTS agent_tool_traces (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    trace_id        TEXT        NOT NULL DEFAULT '',
    execution_id    TEXT        NOT NULL DEFAULT '',
    conversation_id UUID,
    agent_id        TEXT        NOT NULL DEFAULT '',
    user_id         TEXT        NOT NULL DEFAULT '',
    step_index      INT         NOT NULL DEFAULT 0,
    tool_call_id    TEXT        NOT NULL DEFAULT '',
    tool_name       TEXT        NOT NULL DEFAULT '',
    tool_type       TEXT        NOT NULL DEFAULT '',
    provider_type   TEXT        NOT NULL DEFAULT '',
    provider_id     TEXT        NOT NULL DEFAULT '',
    server_id       TEXT        NOT NULL DEFAULT '',
    capability_id   TEXT        NOT NULL DEFAULT '',
    arguments_json  JSONB       NOT NULL DEFAULT '{}',
    raw_result_json JSONB       NOT NULL DEFAULT 'null',
    raw_result_text TEXT        NOT NULL DEFAULT '',
    summary         TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL CHECK (status IN ('success', 'error')),
	error_message   TEXT        NOT NULL DEFAULT '',
	error_code      TEXT        NOT NULL DEFAULT '',
    latency_ms      BIGINT      NOT NULL DEFAULT 0,
    raw_truncated   BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata_json   JSONB       NOT NULL DEFAULT '{}',
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_tool_traces_trace
    ON agent_tool_traces (trace_id, step_index, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_agent_tool_traces_conv
    ON agent_tool_traces (conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_trace_events (
    id                UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    trace_id          TEXT        NOT NULL DEFAULT '',
    execution_id      TEXT        NOT NULL DEFAULT '',
    conversation_id   UUID,
    agent_id          TEXT        NOT NULL DEFAULT '',
    user_id           TEXT        NOT NULL DEFAULT '',
    run_type          TEXT        NOT NULL DEFAULT '',
    observation_type  TEXT        NOT NULL DEFAULT '',
    event_type        TEXT        NOT NULL DEFAULT '',
    step_index        INT         NOT NULL DEFAULT 0,
    span_name         TEXT        NOT NULL DEFAULT '',
    parent_event_id   TEXT        NOT NULL DEFAULT '',
    status            TEXT        NOT NULL DEFAULT '',
    input_json        JSONB       NOT NULL DEFAULT 'null',
    output_json       JSONB       NOT NULL DEFAULT 'null',
    summary           TEXT        NOT NULL DEFAULT '',
    error_message     TEXT        NOT NULL DEFAULT '',
    model             TEXT        NOT NULL DEFAULT '',
    prompt_tokens     INT         NOT NULL DEFAULT 0,
    completion_tokens INT         NOT NULL DEFAULT 0,
    total_tokens      INT         NOT NULL DEFAULT 0,
    cost_usd          DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_ms        BIGINT      NOT NULL DEFAULT 0,
    tool_trace_id     TEXT        NOT NULL DEFAULT '',
    provider_type     TEXT        NOT NULL DEFAULT '',
    provider_id       TEXT        NOT NULL DEFAULT '',
    node_id           TEXT        NOT NULL DEFAULT '',
    node_type         TEXT        NOT NULL DEFAULT '',
    workflow_id       TEXT        NOT NULL DEFAULT '',
    workflow_version  TEXT        NOT NULL DEFAULT '',
    sequence_no       BIGINT      NOT NULL DEFAULT 0,
    metadata_json     JSONB       NOT NULL DEFAULT '{}',
    otel_trace_id     TEXT        NOT NULL DEFAULT '',
    otel_span_id      TEXT        NOT NULL DEFAULT '',
    started_at        TIMESTAMPTZ,
    ended_at          TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_trace_events_trace
    ON agent_trace_events (trace_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_agent_trace_events_conv
    ON agent_trace_events (conversation_id, created_at ASC);

CREATE TABLE IF NOT EXISTS agent_execution_checkpoints (
    id                        UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    execution_id              TEXT        NOT NULL,
    trace_id                  TEXT        NOT NULL DEFAULT '',
    conversation_id           UUID,
    agent_id                  TEXT        NOT NULL DEFAULT '',
    user_id                   TEXT        NOT NULL DEFAULT '',
    current_node              TEXT        NOT NULL DEFAULT '',
    step_index                INT         NOT NULL DEFAULT 0,
    messages_snapshot_json    JSONB       NOT NULL DEFAULT '[]',
    pending_tool_calls_json   JSONB       NOT NULL DEFAULT '[]',
    completed_tool_calls_json JSONB       NOT NULL DEFAULT '[]',
    runtime_state_json        JSONB       NOT NULL DEFAULT '{}',
    status                    TEXT        NOT NULL CHECK (status IN ('running', 'paused', 'waiting_approval', 'completed', 'failed', 'expired')),
    resume_reason             TEXT        NOT NULL DEFAULT '',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_execution_checkpoints_execution
    ON agent_execution_checkpoints (execution_id);
CREATE INDEX IF NOT EXISTS idx_agent_execution_checkpoints_status
ON agent_execution_checkpoints (status, expires_at);

ALTER TABLE agent_execution_checkpoints DROP CONSTRAINT IF EXISTS agent_execution_checkpoints_status_check;
ALTER TABLE agent_execution_checkpoints ADD CONSTRAINT agent_execution_checkpoints_status_check
    CHECK (status IN ('running', 'paused', 'waiting_approval', 'completed', 'failed', 'expired'));

CREATE TABLE IF NOT EXISTS agent_tool_approvals (
    id                UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    execution_id      TEXT        NOT NULL,
    trace_id          TEXT        NOT NULL DEFAULT '',
    agent_id          TEXT        NOT NULL DEFAULT '',
    user_id           TEXT        NOT NULL DEFAULT '',
    tool_call_id      TEXT        NOT NULL,
    server_id         TEXT        NOT NULL,
    tool_name         TEXT        NOT NULL,
    risk_level        TEXT        NOT NULL
        CHECK (risk_level IN ('destructive', 'unclassified')),
    encrypted_payload TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executing', 'executed')),
    decided_by        TEXT        NOT NULL DEFAULT '',
    decision_reason   TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at        TIMESTAMPTZ,
    executed_at       TIMESTAMPTZ,
    expires_at        TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 minutes',
    UNIQUE (execution_id, tool_call_id)
);
CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_pending
ON agent_tool_approvals (status, expires_at, created_at);

-- =============================================================================
-- Static Workflow Engine Stage 1A
-- =============================================================================

CREATE TABLE IF NOT EXISTS workflow_definitions (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    name            TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    draft_revision  BIGINT      NOT NULL DEFAULT 1,
    draft_spec_json JSONB       NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name)
);
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS draft_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS draft_spec_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE IF NOT EXISTS workflow_versions (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    definition_id   UUID        NOT NULL REFERENCES workflow_definitions(id) ON DELETE RESTRICT,
    version_no      BIGINT      NOT NULL,
    name            TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    spec_json       JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (definition_id, version_no)
);
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS definition_id UUID;
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS version_no BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS spec_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_workflow_versions_definition
    ON workflow_versions (definition_id, version_no DESC);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id               UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    definition_id    UUID        NOT NULL REFERENCES workflow_definitions(id) ON DELETE RESTRICT,
    version_id       UUID        NOT NULL REFERENCES workflow_versions(id) ON DELETE RESTRICT,
    version_no       BIGINT      NOT NULL,
    status           TEXT        NOT NULL CHECK (status IN ('queued', 'running', 'pause_requested', 'paused', 'cancel_requested', 'canceled', 'manual_intervention', 'completed', 'failed')),
	generation       BIGINT      NOT NULL DEFAULT 1,
	scheduler_owner TEXT        NOT NULL DEFAULT '',
	lease_expires_at TIMESTAMPTZ,
	pause_reason     TEXT        NOT NULL DEFAULT '',
	cancel_reason    TEXT        NOT NULL DEFAULT '',
	manual_reason    TEXT        NOT NULL DEFAULT '',
	next_event_sequence BIGINT   NOT NULL DEFAULT 1,
    snapshot_json    JSONB       NOT NULL,
    input_json       JSONB       NOT NULL DEFAULT '{}',
    output_text      TEXT        NOT NULL DEFAULT '',
    error_message    TEXT        NOT NULL DEFAULT '',
    idempotency_key  TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS definition_id UUID;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS version_id UUID;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS version_no BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'queued';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS scheduler_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS pause_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS cancel_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS manual_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS next_event_sequence BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS snapshot_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS input_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS output_text TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS request_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;
ALTER TABLE workflow_runs DROP CONSTRAINT IF EXISTS workflow_runs_status_check;
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_status_check
    CHECK (status IN ('queued', 'running', 'pause_requested', 'paused', 'cancel_requested', 'canceled', 'manual_intervention', 'completed', 'failed'));
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_runs_idempotency
    ON workflow_runs (idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs (status, lease_expires_at, created_at ASC);

CREATE TABLE IF NOT EXISTS workflow_node_attempts (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    run_id          UUID        NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id         TEXT        NOT NULL,
    attempt_no      INT         NOT NULL DEFAULT 1,
    status          TEXT        NOT NULL CHECK (status IN ('pending', 'ready', 'claimed', 'running', 'succeeded', 'failed', 'retry_wait', 'skipped', 'paused', 'canceled', 'manual_intervention')),
	run_generation  BIGINT      NOT NULL DEFAULT 1,
	lease_owner     TEXT        NOT NULL DEFAULT '',
	lease_expires_at TIMESTAMPTZ,
	fence_token     BIGINT      NOT NULL DEFAULT 0,
	retry_at        TIMESTAMPTZ,
	effect_class    TEXT        NOT NULL DEFAULT 'pure',
	selected_edges_json JSONB  NOT NULL DEFAULT '[]',
    input_text      TEXT        NOT NULL DEFAULT '',
    output_summary  TEXT        NOT NULL DEFAULT '',
    error_message   TEXT        NOT NULL DEFAULT '',
    error_code      TEXT        NOT NULL DEFAULT '',
    trace_id        TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    UNIQUE (run_id, node_id, attempt_no)
);
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS attempt_no INT NOT NULL DEFAULT 1;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS run_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS fence_token BIGINT NOT NULL DEFAULT 0;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS retry_at TIMESTAMPTZ;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS effect_class TEXT NOT NULL DEFAULT 'pure';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS selected_edges_json JSONB NOT NULL DEFAULT '[]';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS input_text TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS output_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;
ALTER TABLE workflow_node_attempts DROP CONSTRAINT IF EXISTS workflow_node_attempts_status_check;
ALTER TABLE workflow_node_attempts ADD CONSTRAINT workflow_node_attempts_status_check
    CHECK (status IN ('pending', 'ready', 'claimed', 'running', 'succeeded', 'failed', 'retry_wait', 'skipped', 'paused', 'canceled', 'manual_intervention'));
CREATE INDEX IF NOT EXISTS idx_workflow_node_attempts_run
    ON workflow_node_attempts (run_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_workflow_node_attempts_claim
    ON workflow_node_attempts (status, retry_at, lease_expires_at);

CREATE TABLE IF NOT EXISTS workflow_events (
    id          UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    run_id      UUID        NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    sequence_no BIGINT      NOT NULL,
    event_type  TEXT        NOT NULL,
    node_id     TEXT        NOT NULL DEFAULT '',
    attempt_no  INT         NOT NULL DEFAULT 0,
    status      TEXT        NOT NULL DEFAULT '',
    actor_type  TEXT        NOT NULL DEFAULT 'system',
    actor_id    TEXT        NOT NULL DEFAULT '',
    summary     TEXT        NOT NULL DEFAULT '',
    payload_json JSONB      NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, sequence_no)
);
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS sequence_no BIGINT NOT NULL DEFAULT 0;
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS attempt_no INT NOT NULL DEFAULT 0;
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS actor_type TEXT NOT NULL DEFAULT 'system';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS payload_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_workflow_events_cursor ON workflow_events (run_id, sequence_no);

CREATE TABLE IF NOT EXISTS workflow_approvals (
    id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    run_id UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    attempt_id UUID NOT NULL REFERENCES workflow_node_attempts(id) ON DELETE CASCADE,
    run_generation BIGINT NOT NULL,
    reason TEXT NOT NULL,
    risk TEXT NOT NULL DEFAULT '',
    request_summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    decision_actor TEXT NOT NULL DEFAULT '',
    decision_comment TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, node_id, attempt_id)
);
ALTER TABLE workflow_approvals ADD COLUMN IF NOT EXISTS run_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_approvals ADD COLUMN IF NOT EXISTS request_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_approvals ADD COLUMN IF NOT EXISTS decision_actor TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_approvals ADD COLUMN IF NOT EXISTS decision_comment TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_approvals ADD COLUMN IF NOT EXISTS decided_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_workflow_approvals_pending ON workflow_approvals (status, created_at) WHERE status='pending';

CREATE TABLE IF NOT EXISTS workflow_effect_intents (
    id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    run_id UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    attempt_id UUID NOT NULL REFERENCES workflow_node_attempts(id) ON DELETE CASCADE,
    run_generation BIGINT NOT NULL,
    effect_class TEXT NOT NULL CHECK (effect_class IN ('pure','idempotent','non_idempotent')),
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'prepared' CHECK (status IN ('prepared','started','succeeded','failed','unknown')),
    reason TEXT NOT NULL DEFAULT '',
    output_summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, node_id, attempt_id),
    UNIQUE (idempotency_key)
);
ALTER TABLE workflow_effect_intents ADD COLUMN IF NOT EXISTS run_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_effect_intents ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_effect_intents ADD COLUMN IF NOT EXISTS output_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_effect_intents ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_workflow_effect_intents_unknown ON workflow_effect_intents (status, updated_at) WHERE status='unknown';

-- =============================================================================
-- Memory Pipeline tables (async outbox → embedder → enricher)
-- =============================================================================

CREATE TABLE IF NOT EXISTS memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    user_id     TEXT,
    agent_id    TEXT,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_outbox_created ON memory_outbox (created_at);
ALTER TABLE memory_outbox ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE memory_outbox ADD COLUMN IF NOT EXISTS agent_id TEXT;
UPDATE memory_outbox
SET user_id = COALESCE(user_id, payload->>'user_id'),
    agent_id = COALESCE(agent_id, payload->>'agent_id')
WHERE user_id IS NULL OR agent_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_memory_outbox_user_id ON memory_outbox (user_id);
CREATE INDEX IF NOT EXISTS idx_memory_outbox_agent_id ON memory_outbox (agent_id);

CREATE TABLE IF NOT EXISTS memory_summaries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    summary         TEXT NOT NULL,
    covered_until   TIMESTAMPTZ NOT NULL,
    token_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tier TEXT NOT NULL DEFAULT 'recent_months',
    period_start TIMESTAMPTZ,
    period_end TIMESTAMPTZ,
    source_start TEXT NOT NULL DEFAULT '',
    source_end TEXT NOT NULL DEFAULT '',
    source_ids UUID[],
    aggregation_key TEXT,
    importance FLOAT8 NOT NULL DEFAULT 0.5,
    confidence FLOAT8 NOT NULL DEFAULT 0.5,
    status TEXT NOT NULL DEFAULT 'active',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_summaries_conv ON memory_summaries (conversation_id, created_at DESC);
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'user';
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT 'recent_months';
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS period_start TIMESTAMPTZ;
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS period_end TIMESTAMPTZ;
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS source_start TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS source_end TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS source_ids UUID[];
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS aggregation_key TEXT;
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS importance FLOAT8 NOT NULL DEFAULT 0.5;
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS confidence FLOAT8 NOT NULL DEFAULT 0.5;
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE UNIQUE INDEX IF NOT EXISTS uq_memory_summaries_aggregation_key
    ON memory_summaries (aggregation_key) WHERE aggregation_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memory_summaries_history_scope
    ON memory_summaries (user_id, agent_id, scope, tier, status, period_end DESC);

-- Phase 1 active short-term memory: one bounded overwrite snapshot per user/agent scope.
CREATE TABLE IF NOT EXISTS memory_active_snapshots (
    id               UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id          TEXT NOT NULL,
    agent_id         TEXT NOT NULL,
    work_context     TEXT[] NOT NULL DEFAULT '{}',
    personal_context TEXT[] NOT NULL DEFAULT '{}',
    top_of_mind      TEXT[] NOT NULL DEFAULT '{}',
    source           JSONB NOT NULL DEFAULT '{}',
    expires_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version          BIGINT NOT NULL DEFAULT 1,
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    UNIQUE (user_id, agent_id)
);
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS work_context TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS personal_context TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS top_of_mind TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS source JSONB NOT NULL DEFAULT '{}';
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
CREATE UNIQUE INDEX IF NOT EXISTS uq_memory_active_snapshots_scope
    ON memory_active_snapshots (user_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_memory_active_snapshots_scope_expiry
    ON memory_active_snapshots (user_id, agent_id, expires_at DESC)
    WHERE status = 'active';

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
ALTER TABLE agent_mcp_tool_links DROP CONSTRAINT IF EXISTS agent_mcp_tool_links_server_id_fkey;
ALTER TABLE agent_mcp_tool_links ADD CONSTRAINT agent_mcp_tool_links_server_id_fkey
    FOREIGN KEY (server_id) REFERENCES mcp_configs(id) ON DELETE CASCADE;

ALTER TABLE agent_skill_links DROP CONSTRAINT IF EXISTS agent_skill_links_skill_id_fkey;
ALTER TABLE agent_skill_links ADD CONSTRAINT agent_skill_links_skill_id_fkey
    FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE;

ALTER TABLE agent_workspaces DROP CONSTRAINT IF EXISTS agent_workspaces_workspace_id_fkey;
ALTER TABLE agent_workspaces ADD CONSTRAINT agent_workspaces_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES rag_workspaces(id) ON DELETE CASCADE;

-- knowledge_docs: add workspace FK (nullable, existing docs have no workspace)
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS workspace_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'knowledge_docs_workspace_id_fkey'
          AND conrelid = 'knowledge_docs'::regclass
    ) THEN
        ALTER TABLE knowledge_docs ADD CONSTRAINT knowledge_docs_workspace_id_fkey
            FOREIGN KEY (workspace_id) REFERENCES rag_workspaces(id) ON DELETE CASCADE;
    END IF;
END $$;

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

-- agent observability/audit table backfill for existing tenant schemas
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS raw_truncated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS server_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS capability_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS tool_trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS run_type TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS observation_type TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS node_type TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS workflow_version TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS sequence_no BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS otel_trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS otel_span_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
ALTER TABLE agent_execution_checkpoints ADD COLUMN IF NOT EXISTS resume_reason TEXT NOT NULL DEFAULT '';

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
    category        TEXT NOT NULL DEFAULT 'other' CHECK (category IN ('preference', 'skill', 'event', 'state', 'relationship', 'other')),
    confidence      FLOAT8 NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0 AND 1),
    source          TEXT NOT NULL DEFAULT 'llm_extraction' CHECK (source IN ('llm_extraction', 'explicit_user', 'manual_api')),
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
-- Phase 0 structured facts: category / confidence / source provenance.
-- ADD COLUMN ... DEFAULT idempotently backfills existing rows on legacy tenants.
ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'other'
    CHECK (category IN ('preference', 'skill', 'event', 'state', 'relationship', 'other'));
ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS confidence FLOAT8 NOT NULL DEFAULT 0.5
	CHECK (confidence BETWEEN 0 AND 1);
ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'llm_extraction'
    CHECK (source IN ('llm_extraction', 'explicit_user', 'manual_api'));
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
    tsv            tsvector GENERATED ALWAYS AS (to_tsvector('public.chinese_zh', content)) STORED,
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

-- Migrate existing tenants whose knowledge_chunks.tsv column was created with the
-- old 'simple' config (no CJK segmentation). A GENERATED column's expression cannot
-- be ALTERed in place, so we drop the GIN index + column and recreate them against
-- public.chinese_zh. Idempotent: once the expression references chinese_zh the guard
-- skips the rebuild. New tenants create tsv correctly above, so this is a no-op for them.
-- Ordering: must run AFTER knowledge_chunks exists (created above).
DO $$
DECLARE
    gen_expr text;
BEGIN
    SELECT pg_get_expr(ad.adbin, ad.adrelid)
      INTO gen_expr
      FROM pg_attribute a
      JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
     WHERE a.attrelid = 'knowledge_chunks'::regclass
       AND a.attname = 'tsv';

    IF gen_expr IS NOT NULL AND position('chinese_zh' IN gen_expr) = 0 THEN
        DROP INDEX IF EXISTS idx_kc_tsv;
        ALTER TABLE knowledge_chunks DROP COLUMN tsv;
        ALTER TABLE knowledge_chunks ADD COLUMN tsv tsvector
            GENERATED ALWAYS AS (to_tsvector('public.chinese_zh', content)) STORED;
        CREATE INDEX idx_kc_tsv ON knowledge_chunks USING GIN(tsv);
    END IF;
END $$;

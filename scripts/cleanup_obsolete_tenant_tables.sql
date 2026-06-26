-- One-off cleanup: drop obsolete tables from all existing tenant schemas.
-- Run once per tenant: SET search_path = tenant_{id}; then execute this file.
-- Safe to re-run (IF EXISTS guards).

-- Drop child tables before parents (FK order)
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

-- sessions: drop FK column first, then the table
ALTER TABLE memory_entries DROP COLUMN IF EXISTS session_id;
DROP TABLE IF EXISTS sessions;

-- knowledge_docs: fix missing columns (was created without workspace_id/content_hash)
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES rag_workspaces(id) ON DELETE CASCADE;
ALTER TABLE knowledge_docs ADD COLUMN IF NOT EXISTS content_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_docs_hash
    ON knowledge_docs (workspace_id, content_hash) WHERE content_hash <> '';

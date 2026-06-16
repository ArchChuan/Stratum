-- internal/migration/sql/008_memory_pipeline.up.sql
-- Memory pipeline tables are per-tenant only (they reference chat_conversations, memory_entries, entities).
-- Actual DDL lives in pkg/tenantdb/tenant_schema.sql and is applied via ProvisionAllTenantSchemas.
-- This migration is intentionally a no-op in the public schema.
SELECT 1;

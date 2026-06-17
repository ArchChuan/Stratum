-- no-op: agents/skills/mcp_configs are tenant-schema tables, not public schema.
-- UNIQUE(name) already defined inline in pkg/tenantdb/tenant_schema.sql.
SELECT 1;

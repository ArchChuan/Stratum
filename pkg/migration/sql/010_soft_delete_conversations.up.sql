-- no-op: chat_conversations lives in tenant schemas, not public.
-- The deleted_at column is managed by pkg/tenantdb/tenant_schema.sql.
SELECT 1;

-- internal/migration/sql/008_memory_pipeline.down.sql
-- No-op: pipeline tables are per-tenant (managed via tenant_schema.sql / ProvisionAllTenantSchemas).
SELECT 1;

-- Migration 015: Memory v2 Schema
-- All memory v2 tables are tenant-scoped.
-- DDL is defined in pkg/storage/postgres/tenant_schema.sql
-- and applied by ProvisionAllTenantSchemas.

-- This migration serves as a marker for schema version tracking.
-- No operations needed here since tenant_schema.sql is idempotent.

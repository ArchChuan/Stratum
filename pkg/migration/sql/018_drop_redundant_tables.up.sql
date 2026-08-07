-- 删除 public schema 下的历史冗余表（早期 bootstrap 遗留，未在编号迁移中创建）。
-- 这些表名与 tenant 租户 schema 中的表同名（如 models，见
-- pkg/storage/postgres/tenant_schema.sql 的租户级定义），因此必须显式使用
-- public. 限定，防止 search_path 指向租户 schema 的连接上误删租户表。
-- 编号迁移只操作 public schema，不触碰租户 DDL；IF EXISTS 保持幂等。
DROP TABLE IF EXISTS public.models;
DROP TABLE IF EXISTS public.model_providers;
DROP TABLE IF EXISTS public.audit_logs;
DROP TABLE IF EXISTS public.tenant_api_keys;
DROP TABLE IF EXISTS public.invitations;

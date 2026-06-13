-- 为每个新租户 schema 的资源表添加 name 唯一约束。
-- rag_workspaces 已有 UNIQUE(name)，此处补齐其余三张表。
-- 这是 tenant-schema DDL，由 tenantdb.Provision 在创建新租户时执行；
-- 已存在的租户 schema 需手动执行或通过 ALTER TABLE 逐 schema 补充。
ALTER TABLE agents      ADD CONSTRAINT agents_name_unique      UNIQUE (name);
ALTER TABLE skills      ADD CONSTRAINT skills_name_unique      UNIQUE (name);
ALTER TABLE mcp_configs ADD CONSTRAINT mcp_configs_name_unique UNIQUE (name);

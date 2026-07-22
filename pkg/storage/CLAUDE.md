# pkg/storage/ — 存储上下文

> 本地薄入口（git 不追踪）。

`postgres/tenant_schema.sql` 承载所有 tenant-only 表 DDL，由 `ProvisionAllTenantSchemas` 幂等应用。execTenant 强制、search_path 清理、废弃表清理、向量删除规则见下方文档。

@../../docs/agent/migration-tenant.md

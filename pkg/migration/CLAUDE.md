# pkg/migration/ — 迁移上下文

> 本地薄入口（git 不追踪）。

编号迁移（`sql/NNN_*.sql`）**只操作 public schema**，禁止引用 tenant-only 表。tenant DDL 放 `pkg/storage/postgres/tenant_schema.sql`。完整 DDL 放置规则、幂等约束、golang-migrate dirty 修复见下方文档。

@../../docs/agent/migration-tenant.md

# 多租户迁移 & DDL 规则（踩坑总结）

> 涉及 `pkg/migration/sql/`、`pkg/storage/postgres/tenant_schema.sql` 或任何 tenant-scoped 表的改动前必读。

## public vs tenant DDL 放置

- 编号迁移（`pkg/migration/sql/NNN_*.sql`）只操作 **public schema**，禁止引用 tenant-only 表（如 `chat_conversations`、`memory_entries`、`memory_entities`）
- 引用 tenant-only 表的 DDL 必须放 `pkg/storage/postgres/tenant_schema.sql`，由 `ProvisionAllTenantSchemas` 幂等应用到每个租户 schema
- tenant schema 的唯一基线是 `pkg/storage/postgres/tenant_schema.sql`；不要在 `pkg/migration/sql/` 复制一份 tenant DDL

## 历史租户兼容（幂等 DDL）

- 新增表/索引用 `IF NOT EXISTS`；新增列用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`
- 新增 `NOT NULL` 列必须带安全 `DEFAULT`，或先 nullable → 回填 → 加约束
- 向 `CREATE TABLE` 追加列后必须紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` backfill，否则已有租户旧表缺列，后续 INDEX/查询报 `column does not exist`（历史反例：旧实体表 user_id 漏 backfill）
- 任何依赖新列的 INDEX / CONSTRAINT / 查询必须排在 backfill 之后，并用 schema 顺序测试覆盖（反例：先建 `idx_agent_exec_trace` 再补 `trace_id`，旧租户启动失败）
- INSERT 必须与目标表 DDL 逐列核对，尤其 NOT NULL 无 DEFAULT 列（反例：outbox 漏 `message_id` 全量回滚）

## golang-migrate dirty 修复

`force <version>` 将指定版本标记为 clean → 再 `Up()` 从下一版本继续；**勿手改 `schema_migrations` 表**。

## execTenant 强制

- 所有操作 tenant-scoped 表的 struct，每个方法必须通过 `execTenant(ctx, tenantID, fn)` 执行
- 禁止直接调 `r.pool.Exec/Query`（反例：EntityRepo 全量绕过 → SQLSTATE 42P01 relation does not exist）
- port 接口中操作 tenant-scoped 表的方法签名必须含 `tenantID string`；缺失则调用层无法传入，tenant 路由永远无法实现

## 连接池 search_path 清理

任何在 `execTenant` 之外执行 `SET search_path` 的函数，必须在 `conn.Release()` 前执行 `conn.Exec(ctx, "RESET search_path")`，否则脏连接回池导致后续调用者解析错误 schema（反例：`ProvisionTenantSchema` 漏 RESET）。

## 启动路径 SQL 必须 schema 限定

启动期（无 tenant 上下文）执行的 SQL（如 `EnsureDefaultTenant`）必须用 `public.table_name` 全限定，否则 search_path 未定时解析失败。

## 向量数据删除

删除单个 tenant/文档的向量数据：调用 `pkg/storage/milvus.VectorStore` 的 `DeleteByFilter` / `DeleteByDocumentIDs`，不要误删共享 collection。Knowledge workspace 使用独立 collection 时，删除整个 workspace 才调用 `DeleteCollection`；命名规则分别见 `pkg/storage/tenantnaming/milvus.go` 与 `pkg/constants/knowledge.go`。

## 废弃表清理

功能迭代后旧表不会自动消失。每次新功能替代旧存储后必须：

1. 删除旧表 DDL（`tenant_schema.sql`）+ 所有 Go 引用
2. 在租户 schema 定义中追加 `DROP` 语句清理存量租户

判断标准：`rg "table_name" -g '*.go'` 零引用后，再核对 SQL、测试和运行期动态表名，确认无消费者才可删除。

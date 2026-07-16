# internal/agent/infrastructure/persistence

该包提供 Agent 配置、聊天、执行、checkpoint、工具追踪、追踪事件、技能查找和租户设置的 PostgreSQL 实现，并统一租户 schema 执行与敏感数据脱敏。

完整导入路径：`github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence`

```mermaid
flowchart LR
  agent["agent_repo.go<br/>PgAgentRepo · AgentRepo 实现"]
  chat["chat_store.go<br/>PgChatStore · conversation/message 持久化"]
  execution["execution_store.go · checkpoint_store.go<br/>PgExecutionStore · PgCheckpointStore"]
  trace["tool_trace_store.go · trace_event_store.go<br/>PgToolTraceStore · PgTraceEventStore<br/>JSON 限长与敏感字段脱敏"]
  lookup["skill_lookup.go · tenant_settings.go<br/>PgSkillLookup · PgTenantSettings"]
  domain["internal/agent/domain"]
  constants["pkg/constants"]
  tenantdb["pkg/tenantdb"]
  pgx["pgx v5 · pgxpool · pgconn"]
  ext["zap"]
  tests["测试<br/>agent_repo_test.go · chat_store_test.go · tool_trace_store_test.go"]
  agent --> domain
  chat --> domain
  execution --> domain
  trace --> domain
  agent --> tenantdb
  chat --> tenantdb
  execution --> tenantdb
  trace --> tenantdb
  lookup --> tenantdb
  agent --> pgx
  chat --> pgx
  execution --> pgx
  trace --> pgx
  lookup --> pgx
  trace --> constants
  trace --> ext
  tests -.agent_repo_test.-> agent
  tests -.chat_store_test.-> chat
  tests -.tool_trace_store_test.-> trace
```

## 说明

各 `Pg*` 类型的方法集与 agent domain/port 中的仓储契约对应；构造函数接收连接池或窄化 pool 接口。所有租户数据访问经 `tenantdb`/`execTenant` 切换 schema。工具追踪写入前进行 JSON 编码、大小限制和敏感键值脱敏。

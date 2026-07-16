# internal/knowledge/infrastructure/persistence

该包用 PostgreSQL 实现知识工作区、文档与分块仓储，并协调删除租户对应的 Milvus 集合。

完整导入路径：`github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence`

```mermaid
flowchart TB
  chunk["chunk_repo.go<br/>ChunkRepo<br/>子块/父块写入、关键词检索与删除"]
  doc["doc_repo.go<br/>DocRepo<br/>文档 CRUD 与摄取状态机"]
  cleaner["tenant_vector_cleaner.go<br/>TenantVectorCleaner<br/>DropTenantCollections"]
  workspace["workspace_repo.go<br/>WorkspaceRepo<br/>Workspace JSONB 映射与 CRUD"]
  domain["internal/knowledge/domain"]
  ports["internal/knowledge/domain/port"]
  constants["pkg/constants"]
  milvus["pkg/storage/milvus"]
  pg["外部：pgx/v5 + PostgreSQL"]
  tests["测试汇总<br/>workspace_repo_test.go"]
  chunk -.实现.-> ports
  doc -.实现.-> ports
  workspace -.实现.-> ports
  chunk --> domain
  doc --> domain
  workspace --> domain
  cleaner --> milvus
  cleaner --> pg
  chunk --> constants
  pkg -.-> tests
```

各 Repo 通过租户上下文执行 SQL 并把数据库错误/行转换为领域对象；`ChunkRepo` 同时维护 parent-child 块与全文检索。清理器先查询租户工作区，再按集合命名规则删除 Milvus 集合。

# internal/knowledge/application

该包编排知识工作区、异步文档摄取和 RAG 查询，用端口连接解析、切块、嵌入、向量与 PostgreSQL 存储。

完整导入路径：`github.com/byteBuilderX/stratum/internal/knowledge/application`

```mermaid
flowchart TB
  subgraph pkg["knowledge/application"]
    ingest["ingest_service.go<br/>KnowledgeIngest / EmbedResolver<br/>IngestDocument·IngestBatch·DeleteWorkspaceData"]
    mocks["mocks.go<br/>MockVectorStore（应用包内测试辅助实现）"]
    rag["rag_service.go<br/>RAGService / RAGQueryRequest·Result<br/>Query·RetrieveRelevantChunks·BuildPrompt"]
    workspace["workspace_service.go<br/>WorkspaceService<br/>工作区 CRUD、统计、上传编排"]
  end
  domain["internal/knowledge/domain"]
  ports["internal/knowledge/domain/port<br/>Repo / Parser / Embedder / VectorIndex"]
  textchunk["pkg/textchunk"]
  vector["pkg/vector"]
  pg["pkg/storage/postgres"]
  obs["pkg/observability"]
  constants["pkg/constants"]
  ext["外部：zap · google/uuid"]
  tests["测试汇总<br/>ingest_behavior、ingest_service、rag_service 及测试 mocks"]
  workspace --> ingest
  workspace --> domain
  workspace --> ports
  ingest --> ports
  ingest --> textchunk
  ingest --> obs
  rag --> ports
  rag --> vector
  rag --> pg
  rag --> constants
  pkg -.-> tests
```

`KnowledgeIngest` 管理准入信号量、后台任务生命周期和摄取状态，执行 parse→chunk→embed→持久化；`RAGService` 依据查询模式组合向量/关键词结果；`WorkspaceService` 维护 PostgreSQL 与向量集合的创建、删除和统计编排。

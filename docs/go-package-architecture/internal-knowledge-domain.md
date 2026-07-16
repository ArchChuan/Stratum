# internal/knowledge/domain

该包定义知识库文档、分块、工作区聚合、配置白名单/默认值及领域错误。

完整导入路径：`github.com/byteBuilderX/stratum/internal/knowledge/domain`

```mermaid
flowchart LR
  errors["errors.go<br/>工作区、摄取、配置相关 Err*"]
  knowledge["knowledge.go<br/>KB / Document / Chunk"]
  workspace["workspace.go<br/>Workspace / WorkspaceConfig<br/>NewWorkspace·Validate·MergeUpdate"]
  rules["AllowedEmbeddingModels<br/>AllowedChunkingStrategies<br/>AllowedQueryModes 与默认值"]
  workspace --> rules
  workspace --> errors
  knowledge --> workspace
```

`Workspace` 是带配置校验和更新不变量的聚合；`Document`、`Chunk` 与 `KB` 表达摄取数据。该纯领域包没有测试文件和直接项目/第三方依赖。

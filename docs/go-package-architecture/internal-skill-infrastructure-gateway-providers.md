# internal/skill/infrastructure/gateway/providers

把代码、LLM、MCP 与数据库动态 Skill 适配为 gateway.SkillProvider，并支持按冻结版本构建执行器。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway/providers`

```mermaid
flowchart LR
  pkg["providers 包<br/>internal/skill/infrastructure/gateway/providers"]
  api["核心类型 / 接口 / 函数<br/>CodeSkillProvider；LLMSkillProvider；MCPSkillProvider；DBSkillAdapter；Execute/ExecuteVersion；buildSkill；buildSkillFromImplementation"]
  subgraph source[非测试源码]
    f0["code_provider.go"]
    f1["llm_provider.go"]
    f2["mcp_provider.go"]
    f3["registry_adapter.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
    pd1["internal/skill/domain/port"]
    pd2["internal/skill/infrastructure/executors"]
    pd3["internal/skill/infrastructure/executors/code"]
    pd4["pkg/tenantdb"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  pkg -->|直接 import| pd2
  pkg -->|直接 import| pd3
  pkg -->|直接 import| pd4
  subgraph external[关键外部依赖]
    ex0["github.com/jackc/pgx/v5"]
    ex1["github.com/jackc/pgx/v5/pgxpool"]
    ex2["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  tests["测试汇总<br/>registry_adapter_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`、`internal/skill/domain/port`、`internal/skill/infrastructure/executors`、`internal/skill/infrastructure/executors/code`、`pkg/tenantdb`。 关键外部依赖为：`github.com/jackc/pgx/v5`、`github.com/jackc/pgx/v5/pgxpool`、`go.uber.org/zap`。 测试文件合并为一个节点：`registry_adapter_test.go`。

# internal/skill/domain/port

定义 Skill 应用层所需的持久化、构造、分析、HTTP、LLM、MCP 与执行器出向契约及传输结构。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/domain/port`

```mermaid
flowchart LR
  pkg["port 包<br/>internal/skill/domain/port"]
  api["核心类型 / 接口 / 函数<br/>SkillRepo；VersionRepo；SkillFactory；CodeAnalyzer；Executor；Doer；LLMCompleter；MCPInvoker；MCPAdapterReader；SkillInput/SkillRow"]
  subgraph source[非测试源码]
    f0["analyzer.go"]
    f1["executor.go"]
    f2["factory.go"]
    f3["http.go"]
    f4["llm.go"]
    f5["mcp.go"]
    f6["skill_repo.go"]
    f7["version_repository.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  f5 -->|声明或实现| api
  f6 -->|声明或实现| api
  f7 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
  end
  pkg -->|直接 import| pd0
  subgraph external[关键外部依赖]
    ex0["net/http"]
  end
  pkg -->|直接 import| ex0
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`。 关键外部依赖为：`net/http`。

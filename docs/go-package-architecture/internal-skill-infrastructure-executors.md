# internal/skill/infrastructure/executors

实现 HTTP、LLM 与提示词三类可执行 Skill；HTTP 路径使用 SSRF 安全客户端，LLM 路径调用统一网关契约。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors`

```mermaid
flowchart LR
  pkg["executors 包<br/>internal/skill/infrastructure/executors"]
  api["核心类型 / 接口 / 函数<br/>HTTPSkill；LLMSkill；PromptSkill；ValidateSkillURL；NewHTTPSkill；NewLLMSkill；NewPromptSkill；Execute/GetConfig"]
  subgraph source[非测试源码]
    f0["http.go"]
    f1["llm.go"]
    f2["prompt.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/llmgateway/domain"]
    pd1["internal/skill/domain"]
    pd2["pkg/httpclient"]
    pd3["pkg/observability"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  pkg -->|直接 import| pd2
  pkg -->|直接 import| pd3
  subgraph external[关键外部依赖]
    ex0["go.uber.org/zap"]
    ex1["net/http"]
    ex2["text/template"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/llmgateway/domain`、`internal/skill/domain`、`pkg/httpclient`、`pkg/observability`。 关键外部依赖为：`go.uber.org/zap`、`net/http`、`text/template`。

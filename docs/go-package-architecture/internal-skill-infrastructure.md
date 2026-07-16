# internal/skill/infrastructure

实现代码静态安全分析与全局/租户并发信号量，为代码执行器提供基础设施能力。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure`

```mermaid
flowchart LR
  pkg["infrastructure 包<br/>internal/skill/infrastructure"]
  api["核心类型 / 接口 / 函数<br/>NewStaticAnalyzer；staticAnalyzer；Semaphore；NewSemaphore；Acquire/Release；ErrConcurrencyLimit"]
  subgraph source[非测试源码]
    f0["analyzer.go"]
    f1["semaphore.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
    pd1["internal/skill/domain/port"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  tests["测试汇总<br/>analyzer_test.go, semaphore_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`、`internal/skill/domain/port`。 测试文件合并为一个节点：`analyzer_test.go`、`semaphore_test.go`。

# pkg/constants

集中定义跨包共享的 Agent、API、认证、知识、记忆、分页与超时常量，并提供知识集合命名函数。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/constants`

```mermaid
flowchart LR
  pkg["constants 包<br/>pkg/constants"]
  api["核心类型 / 接口 / 函数<br/>CollectionName；DefaultTenantID；AgentExecTimeout；LoopCompaction*；Memory* 常量；分页与上传限制"]
  subgraph source[非测试源码]
    f0["agent.go"]
    f1["api.go"]
    f2["auth.go"]
    f3["knowledge.go"]
    f4["memory.go"]
    f5["pagination.go"]
    f6["timeouts.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  f5 -->|声明或实现| api
  f6 -->|声明或实现| api
  api -->|组成包能力| pkg
  tests["测试汇总<br/>memory_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 测试文件合并为一个节点：`memory_test.go`。

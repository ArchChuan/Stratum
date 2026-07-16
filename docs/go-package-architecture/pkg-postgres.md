# pkg/postgres

为已迁移的 storage/postgres 包保留向后兼容构造入口。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/postgres`

```mermaid
flowchart LR
  pkg["postgres 包<br/>pkg/postgres"]
  api["核心类型 / 接口 / 函数<br/>Pool 别名；New"]
  subgraph source[非测试源码]
    f0["postgres.go"]
  end
  f0 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["pkg/storage/postgres"]
  end
  pkg -->|直接 import| pd0
  subgraph external[关键外部依赖]
    ex0["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  tests["测试汇总<br/>postgres_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`pkg/storage/postgres`。 关键外部依赖为：`go.uber.org/zap`。 测试文件合并为一个节点：`postgres_test.go`。

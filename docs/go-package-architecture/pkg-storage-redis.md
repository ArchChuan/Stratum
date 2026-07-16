# pkg/storage/redis

封装 go-redis 客户端及最小 KVStore 接口，统一 key-not-found 语义。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/storage/redis`

```mermaid
flowchart LR
  pkg["redis 包<br/>pkg/storage/redis"]
  api["核心类型 / 接口 / 函数<br/>Client；KVStore；Store；New/Wrap；NewStore；Get/Set/Del；ErrKeyNotFound"]
  subgraph source[非测试源码]
    f0["client.go"]
    f1["store.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/redis/go-redis/v9"]
    ex1["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  tests["测试汇总<br/>store_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`github.com/redis/go-redis/v9`、`go.uber.org/zap`。 测试文件合并为一个节点：`store_test.go`。

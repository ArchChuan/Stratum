# pkg/storage/tenantnaming

提供无数据库 IO 的租户级 Milvus collection/partition、NATS subject 与 Neo4j label 命名 DSL。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/storage/tenantnaming`

```mermaid
flowchart LR
  pkg["tenantnaming 包<br/>pkg/storage/tenantnaming"]
  api["核心类型 / 接口 / 函数<br/>TenantCollection；KnowledgeCollection；WorkspaceCollection；WorkspacePartition；TenantSubject；TenantLabel"]
  subgraph source[非测试源码]
    f0["milvus.go"]
    f1["nats.go"]
    f2["neo4j.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["pkg/storage/postgres"]
  end
  pkg -->|直接 import| pd0
  subgraph external[关键外部依赖]
    ex0["crypto/sha256"]
  end
  pkg -->|直接 import| ex0
  tests["测试汇总<br/>milvus_test.go, nats_test.go, neo4j_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`pkg/storage/postgres`。 关键外部依赖为：`crypto/sha256`。 测试文件合并为一个节点：`milvus_test.go`、`nats_test.go`、`neo4j_test.go`。

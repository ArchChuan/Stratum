# pkg/tenantdb

向后兼容地重导出 storage/postgres 的租户上下文/事务/schema API 与 storage/tenantnaming 的命名 API。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/tenantdb`

```mermaid
flowchart LR
  pkg["tenantdb 包<br/>pkg/tenantdb"]
  api["核心类型 / 接口 / 函数<br/>TenantContext/Role 别名；ExecTenant；Provision*；TenantCollection；WorkspacePartition；TenantSubject；TenantLabel"]
  subgraph source[非测试源码]
    f0["context.go"]
    f1["milvus.go"]
    f2["nats.go"]
    f3["neo4j.go"]
    f4["postgres.go"]
    f5["schema.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  f5 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["pkg/storage/postgres"]
    pd1["pkg/storage/tenantnaming"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  tests["测试汇总<br/>context_test.go, milvus_test.go, nats_test.go, neo4j_test.go, postgres_unit_test.go, schema_unit_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`pkg/storage/postgres`、`pkg/storage/tenantnaming`。 测试文件合并为一个节点：`context_test.go`、`milvus_test.go`、`nats_test.go`、`neo4j_test.go`、`postgres_unit_test.go`、`schema_unit_test.go`。

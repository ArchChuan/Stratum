# pkg/storage/postgres

封装 pgx 连接池、最小查询接口、租户上下文与 search_path 事务，并执行 public/tenant schema 幂等 provisioning。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/storage/postgres`

```mermaid
flowchart LR
  pkg["postgres 包<br/>pkg/storage/postgres"]
  api["核心类型 / 接口 / 函数<br/>Pool；Querier；TxBeginner；TenantExecer；TenantContext；New/Wrap；ExecTenant；WithTenant/FromContext；Provision*；EnsureDefaultTenant"]
  subgraph source[非测试源码]
    f0["pool.go"]
    f1["querier.go"]
    f2["tenant.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/jackc/pgx/v5"]
    ex1["github.com/jackc/pgx/v5/pgconn"]
    ex2["github.com/jackc/pgx/v5/pgxpool"]
    ex3["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  pkg -->|直接 import| ex3
  tests["测试汇总<br/>pool_test.go, tenant_schema_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`github.com/jackc/pgx/v5`、`github.com/jackc/pgx/v5/pgconn`、`github.com/jackc/pgx/v5/pgxpool`、`go.uber.org/zap`。 测试文件合并为一个节点：`pool_test.go`、`tenant_schema_test.go`。

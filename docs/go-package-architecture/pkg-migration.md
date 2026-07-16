# pkg/migration

封装 golang-migrate 的 public schema PostgreSQL 文件迁移执行，包括 dirty version 修复与增量升级。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/migration`

```mermaid
flowchart LR
  pkg["migration 包<br/>pkg/migration"]
  api["核心函数<br/>RunPublicSchema"]
  subgraph source[非测试源码]
    f0["migration.go"]
  end
  f0 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/golang-migrate/migrate/v4"]
    ex1["github.com/golang-migrate/migrate/v4/database/pgx/v5"]
    ex2["github.com/golang-migrate/migrate/v4/source/file"]
    ex3["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  pkg -->|直接 import| ex3
  tests["占位测试<br/>migration_test.go<br/>仅记录迁移配置说明，未调用 RunPublicSchema"]
  tests -.->|尚未覆盖运行 API| pkg
```

`migration.go` 仅公开 `RunPublicSchema`：它创建 migrate 实例、检查并修复 dirty version、执行 `Up`，再通过传入的 Zap logger 记录完成状态；包内没有额外的 logger 类型。当前包没有直接导入其他 stratum 项目包。关键外部依赖为：`github.com/golang-migrate/migrate/v4`、`github.com/golang-migrate/migrate/v4/database/pgx/v5`、`github.com/golang-migrate/migrate/v4/source/file`、`go.uber.org/zap`。`migration_test.go` 是占位测试，只输出配置说明，未验证 `RunPublicSchema`。

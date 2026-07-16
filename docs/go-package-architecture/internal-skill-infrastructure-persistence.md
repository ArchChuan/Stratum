# internal/skill/infrastructure/persistence

使用 PostgreSQL 租户事务实现 SkillRepo 与 VersionRepo，负责 JSONB 编解码、领域错误翻译和草稿发布事务。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence`

```mermaid
flowchart LR
  pkg["persistence 包<br/>internal/skill/infrastructure/persistence"]
  api["核心类型 / 接口 / 函数<br/>PgSkillRepo；PgSkillVersionRepo；NewPgSkillRepo；NewPgSkillVersionRepo；CRUD；InsertSkillWithDraft；UpdateDraft*；PublishDraft"]
  subgraph source[非测试源码]
    f0["skill_repo.go"]
    f1["skill_version_repo.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
    pd1["internal/skill/domain/port"]
    pd2["pkg/tenantdb"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  pkg -->|直接 import| pd2
  subgraph external[关键外部依赖]
    ex0["github.com/jackc/pgx/v5"]
    ex1["github.com/jackc/pgx/v5/pgconn"]
    ex2["github.com/jackc/pgx/v5/pgxpool"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`、`internal/skill/domain/port`、`pkg/tenantdb`。 关键外部依赖为：`github.com/jackc/pgx/v5`、`github.com/jackc/pgx/v5/pgconn`、`github.com/jackc/pgx/v5/pgxpool`。

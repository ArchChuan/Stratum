# internal/skill/application

编排 Skill 的 CRUD、草稿/版本发布及执行用例，只依赖领域模型与消费者侧 port。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/application`

```mermaid
flowchart LR
  pkg["application 包<br/>internal/skill/application"]
  api["核心类型 / 接口 / 函数<br/>SkillService；VersionService；Executor；SkillRunner；SkillRegistry；Create/Update/Delete；RunSkillTest/RunDraftSkill；PublishDraft"]
  subgraph source[非测试源码]
    f0["executor_service.go"]
    f1["skill_service.go"]
    f2["version_service.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
    pd1["internal/skill/domain/port"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  subgraph external[关键外部依赖]
    ex0["github.com/google/uuid"]
    ex1["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  tests["测试汇总<br/>version_service_test.go, executor_service_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`、`internal/skill/domain/port`。 关键外部依赖为：`github.com/google/uuid`、`go.uber.org/zap`。 测试文件合并为一个节点：`version_service_test.go`、`executor_service_test.go`。

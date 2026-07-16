# internal/skill/domain

定义 Skill 聚合、可执行契约、版本能力/工具契约/实现配置、发布校验与领域错误。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/domain`

```mermaid
flowchart LR
  pkg["domain 包<br/>internal/skill/domain"]
  api["核心类型 / 接口 / 函数<br/>Skill；SkillExecutor；BaseSkill；SkillVersion；Capability；ToolContract；Implementation；ValidatePublishable；AnalysisError"]
  subgraph source[非测试源码]
    f0["defaults.go"]
    f1["errors.go"]
    f2["skill.go"]
    f3["version.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  api -->|组成包能力| pkg
  tests["测试汇总<br/>version_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 测试文件合并为一个节点：`version_test.go`。

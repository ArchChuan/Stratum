# internal/skill/infrastructure/executors/code

实现 Python 子进程与 goja JavaScript VM 的受限代码执行，并将执行器封装为 CodeSkill。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code`

```mermaid
flowchart LR
  pkg["code 包<br/>internal/skill/infrastructure/executors/code"]
  api["核心类型 / 接口 / 函数<br/>CodeExecutorConfig；CodeExecutor；CodeSkill；DefaultCodeExecutorConfig；NewCodeExecutor；NewCodeSkillWithExecutor；Execute；ErrExecutionTimeout"]
  subgraph source[非测试源码]
    f0["executor.go"]
    f1["skill.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["internal/skill/domain"]
    pd1["internal/skill/infrastructure"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  subgraph external[关键外部依赖]
    ex0["github.com/dop251/goja"]
    ex1["os/exec"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  tests["测试汇总<br/>executor_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`internal/skill/domain`、`internal/skill/infrastructure`。 关键外部依赖为：`github.com/dop251/goja`、`os/exec`。 测试文件合并为一个节点：`executor_test.go`。

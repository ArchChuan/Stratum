# internal/platform/runtime

承接应用启动期的运行时编排：初始化追踪、引导租户 schema、注册后台组件与 HTTP 服务，并响应进程退出信号。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/platform/runtime`

```mermaid
flowchart LR
  pkg["runtime 包<br/>internal/platform/runtime"]
  api["核心类型 / 接口 / 函数<br/>InitTracingFromEnv；BootstrapTenants；Run；registerMemoryPipeline；registerMemoryWorkers；registerChatCleanup；registerGuestReaper；registerHTTPServer"]
  subgraph source[非测试源码]
    f0["runtime.go"]
  end
  f0 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["api/http"]
    pd1["api/wiring"]
    pd2["config"]
    pd3["internal/iam/application"]
    pd4["internal/platform/harness"]
    pd5["pkg/constants"]
    pd6["pkg/observability"]
    pd7["pkg/tenantdb"]
  end
  pkg -->|直接 import| pd0
  pkg -->|直接 import| pd1
  pkg -->|直接 import| pd2
  pkg -->|直接 import| pd3
  pkg -->|直接 import| pd4
  pkg -->|直接 import| pd5
  pkg -->|直接 import| pd6
  pkg -->|直接 import| pd7
  subgraph external[关键外部依赖]
    ex0["github.com/jackc/pgx/v5/pgxpool"]
    ex1["go.uber.org/zap"]
    ex2["net/http"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`api/http`、`api/wiring`、`config`、`internal/iam/application`、`internal/platform/harness`、`pkg/constants`、`pkg/observability`、`pkg/tenantdb`。 关键外部依赖为：`github.com/jackc/pgx/v5/pgxpool`、`go.uber.org/zap`、`net/http`。

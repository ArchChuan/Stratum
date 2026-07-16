# pkg/reqctx

用 context 传播 trace ID 与 tenant ID，并提供对应的安全读取函数。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/reqctx`

```mermaid
flowchart LR
  pkg["reqctx 包<br/>pkg/reqctx"]
  api["核心类型 / 接口 / 函数<br/>WithTraceID；TraceIDFromContext；WithTenantID；TenantIDFromContext"]
  subgraph source[非测试源码]
    f0["context.go"]
  end
  f0 -->|声明或实现| api
  api -->|组成包能力| pkg
```

图中 `context.go` 定义两组 context 写入与读取函数：`WithTraceID` / `TraceIDFromContext` 和 `WithTenantID` / `TenantIDFromContext`。当前包没有直接导入其他 stratum 项目包。

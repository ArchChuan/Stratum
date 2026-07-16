# pkg/httpclient

构造统一超时、User-Agent、重试与 SSRF 防护的 HTTP 客户端和 Transport。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/httpclient`

```mermaid
flowchart LR
  pkg["httpclient 包<br/>pkg/httpclient"]
  api["核心类型 / 接口 / 函数<br/>Doer；Option；New；NewSSRFSafe；WithTimeout；WithRetry；retryRoundTripper；uaTransport；ssrfSafeDial"]
  subgraph source[非测试源码]
    f0["client.go"]
    f1["transport.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["net/http"]
  end
  pkg -->|直接 import| ex0
  tests["测试汇总<br/>transport_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`net/http`。 测试文件合并为一个节点：`transport_test.go`。

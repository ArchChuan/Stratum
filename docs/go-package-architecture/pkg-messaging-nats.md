# pkg/messaging/nats

封装 NATS 连接和带退避重试的消息发布。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/messaging/nats`

```mermaid
flowchart LR
  pkg["nats 包<br/>pkg/messaging/nats"]
  api["核心类型 / 接口 / 函数<br/>Connect；Publisher；JetStreamPublisher；NewJetStreamPublisher；Publish"]
  subgraph source[非测试源码]
    f0["conn.go"]
    f1["publisher.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/nats-io/nats.go"]
  end
  pkg -->|直接 import| ex0
```

`conn.go` 建立 NATS 连接；`publisher.go` 定义 `Publisher` 接口，并由 `JetStreamPublisher` 实现，构造入口为 `NewJetStreamPublisher`，发布失败时执行有限指数退避。当前包没有直接导入其他 stratum 项目包。关键外部依赖为：`github.com/nats-io/nats.go`。

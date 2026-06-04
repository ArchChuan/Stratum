# NATS / Hermes Development Rules

## 架构说明

`internal/hermes/client.go` 封装 NATS 客户端，对上层暴露 `Publish` / `Subscribe` 接口。应用层通过 `hermes.Client` 操作，不直接使用 NATS SDK。

## Connection Config

- URL 格式：`nats://host:4222`
- JetStream 模式默认启用
- 断线重连：SDK 内置自动重连，无需手动实现
- 连接失败：`config.InitializeServices` 中仅 Warn 不阻断启动

## Subject Naming Convention

```
{domain}.{action}[.{qualifier}]
```

示例：

- `skill.executed` — Skill 执行完成
- `agent.started` — Agent 开始执行
- `memory.persisted` — 记忆持久化完成
- `knowledge.ingested` — 知识摄入完成

## Event Structure

```go
type Event struct {
    ID        string
    Type      string        // Subject 名称
    Timestamp time.Time
    Source    string        // 发送方组件名
    Data      interface{}
}
```

## Usage Patterns

### Publish Event

```go
client.Publish(hermes.Event{
    Type:   "skill.executed",
    Source: "skill-executor",
    Data:   result,
})
```

### Subscribe to Events

```go
client.Subscribe("memory.*", func(event hermes.Event) {
    // 处理事件，应快速返回
})
```

订阅支持通配符，`*` 匹配单段，`>` 匹配多段（NATS 标准）。

## Rules

1. **Handler 不阻塞**：事件处理应快速返回，重型操作放入独立 goroutine
2. **幂等性**：消息可能重复投递，handler 必须是幂等的
3. **错误处理**：Subscribe handler 内部捕获 panic，记录日志但不中断订阅
4. **连接失败**：NATS 不可用时仅 Warn，服务正常启动；相关功能降级处理
5. **Subject 命名**：遵守 `domain.action` 格式，不使用空格和特殊字符

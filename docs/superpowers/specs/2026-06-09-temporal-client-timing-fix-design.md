# Temporal Client Timing Fix — Design Spec

**日期**: 2026-06-09
**分支**: feat/tenant-suspend-enforcement
**作者**: byteBuilderX

---

## 问题

`main.go` 在 harness 启动前调用 `temporalWorker.Client()`，此时 `TemporalWorkerComponent.Start()` 尚未执行，`c.client` 为 `nil`。

```
main.go:171  api.SetupRouter(..., temporalWorker.Client())  // nil
router.go:165  agentRegistry.SetTemporalClient(nil)          // 写入 nil
agent.go:284  if a.TemporalClient == nil { return error }    // 执行时报错
```

结果：即使 API key 填好、Temporal server 跑起来，所有 ReAct agent 执行都失败。

---

## 根因

`TemporalWorkerComponent.Client()` 返回 `c.client`（`client.Client` 接口值），而不是组件自身。组件在 harness goroutine 的 `Start()` 里才给 `c.client` 赋值，但 `SetupRouter` 在 harness 启动前调用，拿到的是 nil 接口值。

---

## 方案 A1（选定）

让 `*TemporalWorkerComponent` 实现 `agent.TemporalWorkflowStarter` 接口，然后把组件本身传给 `SetupRouter`，代替 `temporalWorker.Client()`。

**优点**：

- 调用时（HTTP 请求进来时）harness 已启动，`c.client` 已就绪
- 最小改动：2 处修改，0 处新文件
- 语义清晰：传的是"能执行 workflow 的组件"，而非瞬时 nil client

**关键性质**：`TemporalWorkerComponent.ExecuteWorkflow` 在运行时才解引用 `c.client`，不在构造时。

---

## 变更范围

### 1. `internal/agent/workflow/worker.go`

新增 `ExecuteWorkflow` 方法，实现 `agent.TemporalWorkflowStarter`：

```go
func (c *TemporalWorkerComponent) ExecuteWorkflow(
    ctx context.Context,
    options client.StartWorkflowOptions,
    workflow interface{},
    args ...interface{},
) (client.WorkflowRun, error) {
    if c.client == nil {
        return nil, fmt.Errorf("temporal-worker: client not initialized (worker not started)")
    }
    return c.client.ExecuteWorkflow(ctx, options, workflow, args...)
}
```

### 2. `cmd/server/main.go`

```diff
- router := api.SetupRouter(cfg, logger, registry, gateway, pgPool.DB(), redisClient.Client(), temporalWorker.Client())
+ router := api.SetupRouter(cfg, logger, registry, gateway, pgPool.DB(), redisClient.Client(), temporalWorker)
```

`SetupRouter` 签名已接受 `agent.TemporalWorkflowStarter`，无需修改。

---

## 不变更的内容

- `router.go` 中 `if temporalClient != nil { agentRegistry.SetTemporalClient(temporalClient) }` 保持不变 — `temporalWorker` 是非 nil 指针，条件成立
- `agent.Registry` 的 `SetTemporalClient` 保持不变
- `BaseAgent.Execute` 中 `TemporalClient == nil` 检查保持不变

---

## 成功标准

1. `go build ./cmd/server/...` 编译通过
2. `go test -race ./internal/agent/workflow/... ./internal/agent/...` 全绿
3. 设置 `QWEN_API_KEY` 或 `ZHIPU_API_KEY`，启动 Temporal server，`POST /agents/:id/execute` 能返回 LLM 响应

---

## 边界情况

- **Temporal server 未启动**：`TemporalWorkerComponent.Start()` 返回错误，`c.client` 仍 nil，`ExecuteWorkflow` 返回明确错误信息
- **并发**：`c.client` 在 `Start()` 时写入一次，此后只读，无竞态

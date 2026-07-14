# 后端 Go 规范与踩坑红线

## 基础规范

- 行宽 ≤120 · import 顺序：stdlib → third-party → internal · 圈复杂度 ≤10
- 日志：Zap only，禁止 `fmt.Print`；字段/级别规范见 [observability.md](observability.md)
- 错误：`fmt.Errorf("operation: %w", err)` 逐层包裹；瞬态错误指数退避（base 100ms，上限 10s）；外部依赖加熔断
- Handler 只解析请求 + 调 Service + 组装响应；业务逻辑在 Service 层

## 并发 & Context 红线（踩坑：MCP performHealthCheck，2026-06-25）

- **`WithTimeout` 禁止在循环外创建后被循环体共用**：每次迭代内 `ctx, cancel := context.WithTimeout(...)`，否则所有迭代串行共用同一 deadline，后续迭代拿到已超时的 context
- **重连/替换有状态对象用 `NewXxx()` 重建并写回**：不要复用已 close 的旧 client 结构体
- **N 个 IO/网络操作并发**：`wg.Add(1)` + goroutine，禁止串行阻塞
- **goroutine 生命周期用 WaitGroup 跟踪**（反例：TenantWatcher，2026-06-27）：`Stop()` 必须 `wg.Wait()`；启动 goroutine 前 `wg.Add(1)`，defer `wg.Done()`
- **共享指针 TOCTOU**（反例：VectorStore.client，2026-06-27）：不要 `if vs.client == nil { ensureConnected() }; use vs.client`——check 与 use 之间 client 可能被换走。用 `getClient(ctx)` 在锁内取出局部副本再用
- **带缓冲 channel + ctx 超时必须排水**（反例：doConnect，2026-06-27）：`ctx.Done()` 提前返回后，后台 goroutine 仍会写 `resultCh`，无人读取则泄漏 gRPC 连接。必须 `select { case res := <-resultCh: if res.err == nil { res.client.Close() } }` 排水
- **早期错误路径必须 drain WaitGroup**（反例：Pipeline.Start，2026-06-27）：中途 `cancel()` 返回前必须 `p.wg.Wait()`，否则已启动的 goroutine 悬挂

## pgx v5 JSONB 编码

- 写 JSONB 字段：先 `b, _ := json.Marshal(v)`，再以 `string(b)` 作为参数传入——pgx v5 对 `[]byte` 与 `string` 的 JSONB 推断不同
- 用 `JSONCodec`（如 `encodePlan`）统一编解码；避免直接构造 `pgtype.JSONB{}`（需正确 OID，OID=0 会编码失败）

## 超时分层

agent 子操作使用 `pkg/constants/timeouts.go` 分级常量：

| 操作 | 超时 |
|------|------|
| DB 读写 | 5s |
| 记忆注入 | 10s |
| RAG · Recall | 15s |
| LLM 非流式 | 60s（flat cap） |

**LLM 流式禁止 flat timeout**：改用 `ResponseHeaderTimeout`(30s) + `LLMStreamIdleTimeout`(30s/token) + `AgentExecTimeout`(90s) 组合，避免长响应被整体 timeout 掐断。

## 删除策略

- AI 业务数据（facts / entries / conversations）**硬删**
- audit log **软删**
- 清除类操作逐一确认清除范围（反例：ClearUserMemories / ClearAgentMemories 需覆盖 memory_entries / entities / conversations，勿反复修补漏表）

## 新增 tenant-scoped 表 struct 三件套

1. 每个方法通过 `execTenant(ctx, tenantID, fn)` 执行（见 [migration-tenant.md](migration-tenant.md)）
2. port 接口方法签名含 `tenantID string`
3. JSONB 字段用 `JSONCodec` 编解码

## 接口新增方法后

同步更新所有 mock/stub 实现者，否则整包编译失败（反例：DeleteAllByUser 漏改 MockEntityRepo）。定位：`grep -r "InterfaceName" --include="*_test.go"`。

## 测试

- 覆盖率 ≥80%，表驱动测试，mock 所有外部依赖，完整套件开 `-race`
- **代码是主，测试是从**：测试断言与产品需求不符 → 改测试；实现违背需求 → 改代码。禁止"测试不过就改代码凑绿"
- AI 写测试必须提供模板文件（如 `api/handler/tenant_handler_test.go`），指定"按这个模式给 X 写完整测试"，不要自由发挥

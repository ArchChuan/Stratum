# Agent SSE 断线续传设计

> 日期：2026-09-17
> 状态：设计已确认，待实施
> 风险分级：R3（命中 `agent-tool-chain` / `eval-touched` / `external-dependency` / `migration`）

## 1. 背景与目标

### 目标

让 Agent 的 SSE 流在断线重连后**接着上次的位置继续输出**，而不是从 checkpoint 快照恢复后整段重新生成。

验收场景以 **F5 页面刷新**为准：用户看到的内容从「已经渲染到的位置」继续，而不是从头重来。

### 非目标

- 不改审批（tool approval）的语义与流程
- 不做前端的已渲染文本持久化（见 §13）
- 不引入新的事件总线（复用现有 Redis）

## 2. 现状（代码证据）

### 2.1 已经具备的

| 能力 | 位置 |
|---|---|
| 执行 context 已与请求 context 解耦 | `internal/agent/application/agent_execution.go:364` `context.WithCancel(context.WithoutCancel(streamCtx))` |
| 恢复键首帧已下发 | `api/http/handler/agent_exec_handler.go:177-180` |
| 刷新后权威发现端点 | `GetActiveExecution`，`agent_exec_handler.go:90-109` |
| 断线自动重连 + 指数退避 | `web/src/modules/agent/api/agent.api.ts:182-209`（1s→10s，5 次，收 token 重置） |
| SSE 信封解析 | `web/src/services/client.ts:225-229`（已解析 `id:` / `event:`） |
| `Last-Event-ID` 的完整实现 | `client.ts:313-341` `streamApiGet`（**仅 workflow 链路在用**） |
| 分代栅栏字段 | `agent_execution_checkpoints.run_generation`，`tenant_schema.sql:1017-1053` |
| 租约 CAS 的成熟范式 | `internal/workflow/application/runtime.go`、`internal/workflow/infrastructure/persistence/store.go:480-541` |

### 2.2 缺失的

| 缺失 | 证据 |
|---|---|
| **执行被客户端断连杀死** | `agent_exec_handler.go:191-193` 在 `clientCtx.Done()` 时显式 `cancel()` |
| **产出不可重放** | agent 侧零事件表、零 Redis Stream；`agent_trace_events` / `agent_executions` 表已 DROP |
| **SSE 无 `id:` 行** | `sse_writer.go:87-98` 的 `write()` 只写 `event:` / `data:` |
| **token 帧无任何序号** | `agent_exec_handler.go:144` payload 仅 `{"token":...}` |
| **checkpoint 不含 LLM 中间输出** | `graph/plan_checkpoint.go:29-40` `ExtractToolMessages` 只留 `role=="tool" \|\| len(ToolCalls)>0` |
| **续跑的结构就是重新生成** | `resumeFromCheckpoint`（`agent.go:2553-2579`）只恢复 messages/plan/actives，**从不使用 `StepIndex`/`CurrentNode` 跳步**；图入口恒为 `nodeLLM`（`graph/react_llm.go:54`） |
| **前端无游标、重连不清缓冲** | `client.ts:302` 丢弃信封；`ChatStreamContext.tsx:124` 无条件 `s.content = ''` |
| **无停止生成入口** | `ChatStreamContext.tsx:200-209` `cancelStream` 是死代码，全仓库无 UI 调用 |

### 2.3 现网故障（本设计要修的）

1. **F5 后从头生成**：`doFreshResume` → `startStream` → `s.content = ''` → 服务端从 checkpoint 重跑 → 几十秒后重新输出一遍
2. **网络抖动时文本重复**：`scheduleReconnect` 内部重连不经 `startStream`，`content` 不清零；服务端从 checkpoint 重跑并重发 → 已渲染部分被**追加一遍**
   - 注：`agent.api.ts:173-177` 的注释「只流新增 token，前端累积 = 完整答案（无重复）」**与实现不符**

## 3. 核心结论

> 想要「接着上次继续输出」，**游标必须来自输出流本身**。
> checkpoint 是波次粒度快照，结构上无法表达波次内部的进度；前端字符计数跨 pod 无意义。

因此需要一个**可寻址的输出日志**。本设计选用 **Redis Stream**。

### 方案对比（已决策）

| | **A. Redis Stream（选定）** | B. PG 事件表 | D. 执行侧缓冲 + 周期快照 |
|---|---|---|---|
| `id:` 来源 | `XADD` 返回的 entry ID 直接用 | 需序列分配器 | 快照边界标记帧 |
| 实时 tail | `XREAD BLOCK` 原生推送 | 需 `LISTEN/NOTIFY`（仓库零使用）或轮询 | 需轮询 |
| 写放大 | 每 token 1 次 Redis | 每 token 1 次 **PG 写** | 每 N 字符 1 次 |
| 仓库内参照 | 无 | 有（`workflow_events`） | 无 |
| 前端改动 | 最小（`Last-Event-ID` 模式已存在） | 同 A | 最大（offset 对齐 + 回滚） |

**选 A 的理由**：这个流的定位是**尽力而为的显示缓冲，不是事实源**（事实源是 checkpoint + 最终落库的 assistant 消息）。既然不是事实源，PG 的事务一致性优势用不上，而它的写成本劣势（序列分配器 + 高写放大 + 清理 worker）照单全收。

## 4. 架构

### 4.1 一句话

**run 的生命周期与 HTTP 请求解绑；HTTP 请求降级为「订阅者」。执行侧只往 Redis Stream 写，订阅侧只从 Redis Stream 读。**

### 4.2 三类参与者

| 角色 | 是谁 | 生命周期由什么决定 |
|---|---|---|
| **runner** | 唯一持有租约、在跑 run 的 goroutine | 租约 + 孤儿超时，**与 HTTP 无关** |
| **subscriber** | 任意持有 SSE 连接的 HTTP 请求 | 浏览器连接 |
| **controller** | 发起「停止生成」的 HTTP 请求 | 一次性 |

### 4.3 数据流

```
LLM token
  └─ tokenCb (执行侧)
       └─ XADD agent:stream:{t}:{eid}:{gen} MAXLEN ~ 20000 * e <事件名> d <JSON>
            └─ entryID (1726483200000-37)  ← 直接作为 SSE 的 id:

订阅侧（任意 pod、任意 HTTP 请求）
  ├─ XRANGE agent:stream:{t}:{eid}:{gen} (cursor +   ← 回放
  └─ XREAD BLOCK 0 STREAMS ... $                     ← 跟流
       └─ 转 SSE: id:<entryID> / event:<e> / data:<d>
```

### 4.4 执行侧不再直接写 socket

今天 `tokenCb` 直接 `writer.EnqueueData`（`agent_exec_handler.go:143-146`），走内存 channel 到本 pod 的 socket。

**`sseEventWriter.enqueue` 是持锁阻塞写**（`sse_writer.go:45-53`：`w.mu.Lock()` + `w.events <- ev`，cap 128），被 LLM 读取循环同步调用（`openai_compat.go:575 scanStream`）。**所以今天一个慢客户端会通过背压一路卡住 LLM 读循环。**

改为只写 Redis 后：

- 慢客户端的背压止步于它自己的订阅 goroutine
- `attach` 与 `remote-tail` **合并成一套逻辑**（同 pod 也走 Redis，「持有者是不是我」不再需要分支）
- 代价 1：token→socket 多一次 Redis 往返（集群内亚毫秒 vs token 间隔几十毫秒）
- 代价 2：**无订阅者时仍要全量 XADD**（今天无订阅者时 `closed=true`，零开销）——这是孤儿超时存在的意义

## 5. 关键决策

### D1. 执行侧不再直接写 socket

见 §4.4。**这是本次改造最大的简化来源**。

### D2. 租约放 PG，不放 Redis

失败模式对比：Redis 挂了时，租约在 Redis → **双跑**；租约在 PG → 只是**降级为重新生成**。后者安全得多。Redis 的职责收窄成「只管流」。

**为什么必须有租约**——它回答的是「此时此刻除了我，还有没有别人在跑这个 execution」，这是进程内状态无法回答的：

| 场景 | 进程内能答吗 |
|---|---|
| F5 重连打到另一个 pod | ✗ |
| pod 滚动更新 / HPA 缩容，旧 pod 还活几秒 | ✗ |
| pod 重启后新进程看到 `status='running'` 的 checkpoint | ✗（该续跑？还是别人在跑？） |
| 同一 pod 内两个 HTTP 请求 | ✓ |

**关键**：今天不双跑，是靠两个行为互相抵消的意外平衡——`cancel()` 在 t1 杀掉了 pod A 的 run，所以 t2 起新的不会撞上。删掉 `cancel()` 的那一刻：

| 是否有进程内注册表 | 落到 pod A | 落到 pod B |
|---|---|---|
| 都没有 | **双跑** | **双跑** |
| 只有注册表 | attach ✓ | **双跑** |
| 注册表 + 租约 | attach ✓ | attach ✓ |

→ **租约不是「多副本的优化」，是这次改造的正确性依赖项**（线上 `stratum` 为 2 副本，HPA 2↔4；frontend nginx `proxy_pass http://stratum-ai:80/` 无 session affinity → 重连各约 50%）。

**owner 标识 = `conversation_id`。** 它本就是 `agent_execution_checkpoints` 的列，因此**不需要新增 `claimed_by` 列**——真正的互斥来自 `run_generation` 的 CAS（fencing token），owner 只是诊断信息。workflow 需要 `scheduler_owner` 是因为它做 work-stealing（任何 pod 抢任何 run）；agent 是请求驱动、`execution_id` 已知，谁抢到由「谁 bump 了 generation」唯一确定。

### D3. 租约的 fencing 必须同时覆盖数据库和流

租约过期后僵尸 runner **不会自己停**。数据库侧写回会被 generation fence 挡掉（安全），但**流不会**——订阅者会读到新旧两个 runner 交错的 token。两条一起上：

| 机制 | 挡什么 |
|---|---|
| 心跳续租（`interval = lease/3`），**续租失败立刻自取消** | 僵尸继续烧 token（窗口 ≤ lease/3） |
| **流 key 带 generation** | 交错（零窗口：物理上写不同 key） |

单靠心跳有 ≤10s 窗口会交错；单靠分 key 则僵尸一直在烧钱。

### D4. 租户命名空间

**`execution_id` 是客户端可控的**：

```go
// internal/agent/application/agent_trace.go:49-54
func executionIDOrNew(id string) string {
	if id == "" { return uuid.Must(uuid.NewV7()).String() }
	return id            // ← 调用方给什么就用什么
}
```

今天安全是因为所有读取都走租户限定的 `GetLatest(ctx, tenantID, executionID)`。**若 Redis key 只由 `execution_id` 构成，客户端提交别人的 execution_id 就能读走别人的 token 流。** 两条硬约束：

1. key 必须过租户命名空间——新增 `pkg/storage/tenantnaming/redis.go`，形状照 `nats.go` 的 `TenantSubject(ctx, subject) (string, error)`（租户从 ctx 取，缺失即报错，**fail closed**）
2. 订阅前必须做一次租户限定的 checkpoint 查询；查不到 → 404，**不区分「不存在」与「不属于你」**，沿用 `GetActiveExecution` 已确立的 existence-oracle 关闭策略

### D5. 时长必须可注入

`AgentExecutionOrphanTimeout`（2min）与租约时长若写成包级 const 直接读，跨实例测试每个都要跑 2 分钟量级。

→ **做成 `AgentService` 的可注入配置，默认值取自 `pkg/constants/agent.go`。** 这同时符合 CLAUDE.md 的常量外置规则。

### D6. 多标签页语义从「抢占」变为「多订阅」

`run_generation` 的字段注释写着「双 tab/设备抢占只有一方胜出」。新设计下这个语义不再需要：

- 两个 tab 都 attach → 两个 viewer → **都实时渲染同一个回答**（今天是互相抢占）
- 仍然只有一个 runner（租约 CAS 保证）
- 任一个 tab 点停止 → 两个都停

## 6. 协议

### 6.1 Key

```
agent:stream:{tenant}:{execution_id}:{gen}
agent:ctrl:{tenant}:{execution_id}          ← Pub/Sub 控制通道
```

**带 gen 的独立 key**，不是单 key + `g` 字段。理由：单 key 方案下租约过期窗口内僵尸 runner 的 token 会混进同一条流，订阅侧必须逐条过滤；独立 key 让交错**在物理上不可能**，reset 判定也从「启发式」变成「精确比对」。

### 6.2 Entry

```
XADD agent:stream:{t}:{eid}:{gen} MAXLEN ~ 20000 * e <事件名，可空> d <JSON>
                        └─ 返回值 1726483200000-37 直接就是 SSE 的 id:
```

`d` 装的就是今天原封不动塞进 `data:` 的那个 JSON。零转换，wire 格式不变。

### 6.3 SSE 帧映射

```
id: <entryID>          ← Redis 原始 ID，不做任何编码
event: <e>             ← 空则整行省略
data: <d>
```

**`id:` 保持为逐字的 Redis 游标**，可直接喂给 `XRANGE key (1726483200000-37 +`，无需编解码。

**哪些帧进流**：meta / token / delegate 进度 / done / error / approval_required **进流**；`: heartbeat` 注释帧**不进流**（纯传输层保活，由订阅侧本地 tick 生成，`SSEHeartbeatInterval` 不变）；reset 由订阅侧合成（见 §6.6）。规则：**流承载 run 的语义产出，传输层保活与游标翻译都是订阅侧的本地职责。**

**心跳有两种，不要混淆**：面向客户端的 SSE `: heartbeat` 注释帧（传输层保活），以及面向 runner 的 `{"type":"viewer"}` 控制消息（见 §7.1，用于孤儿判定）。两者都搭同一个 5s tick，但走不同通道、语义完全无关。

### 6.4 游标协议

前端用 POST + fetch（`streamApiEvents`），非 `EventSource`，故 SSE 的 header 惯例无约束力。**游标走 body，不走 header**：

```jsonc
// 重连请求体
{ "execution_id": "...", "generation": 1, "last_event_id": "1726483200000-37" }
```

`generation` 从每次订阅的**首帧**获得——扩展已有恢复键帧（事件名 `meta`）：

```jsonc
{ "execution_id": "...", "generation": 1 }   // 今天只有 execution_id
```

服务端判定：

```
genNow    = checkpoint.run_generation
genClient = body.generation      // 缺失视为 0

genClient != genNow  → 发 reset 帧，从流的开头全量回放当前 gen
genClient == genNow  → XRANGE key (last_event_id +  增量回放 → XREAD BLOCK 接实时
```

缺失 `generation` 时**一律 reset**——连续性上 fail closed。

### 6.5 游标的存活域（重要）

**`last_event_id` 只存内存，不跨页面重载。**

F5 后 React 状态全部丢失，且 assistant 消息**不在数据库里**（`agent.go:721` 只在 run 结束后写一次，`:1767` 出错直接 early-return），`restoreFromActive` 只能建空占位气泡 → `useChatPage.ts:561` 的 `content: accumulatedContent || m.content` 拿到的是**空串**。此时只回放增量会给用户**半截答案**。

| 场景 | React 状态 | 发什么游标 | 服务端行为 | 视觉 |
|---|---|---|---|---|
| 网络抖动（`agent.api.ts` 内部自动重连） | 存活，已渲染 N 字符 | `last_event_id` | **增量**回放 | 真·无缝，无重复 |
| F5 / 重开页 | 丢失 | **不发 `last_event_id`**（只发 `generation`） | **全量**回放当前 gen | 毫秒级瞬间补全，然后继续流式 |
| 跨 generation（pod 重启） | 任意 | gen 不匹配 | reset + 全量回放新 gen | 清空重渲染 |

**所以游标不是持久化状态，是会话内状态。** sessionStorage 只放 `{executionId, generation}`。

**连带**：`AgentStreamMaxLen` 取 **20000**（不是 10000）。因为 F5 需要全量回放，裁剪造成的缺口会直接截断用户看到的答案。

### 6.6 reset 帧

```jsonc
{ "reset": true, "generation": 2, "reason": "generation_changed" | "stream_lost" }
```

**reset 由订阅侧合成，不写入流**——它是订阅者对「游标与当前 generation 不匹配」的翻译，不是 run 的产出。

前端收到 → 清空当前 assistant 气泡 → 从头渲染后续 token。**这是全协议里唯一的新概念**，只在 pod 真重启 / 租约真过期时出现。

裁剪产生缺口时（游标早于现存最老 entry）同样按 `stream_lost` 发 reset，**不发带洞的文本**。

### 6.7 TTL 与容量

| 参数 | 值 | 位置 |
|---|---|---|
| `AgentStreamTTL` | 1h | `pkg/constants/agent.go` |
| `AgentStreamMaxLen` | 20000（`MAXLEN ~` 近似裁剪） | 同上 |
| `AgentExecutionLeaseTTL` | 30s（续租 interval = 10s = `TTL/3`） | 同上 |
| `AgentViewerTimeout` | 15s（viewer 无心跳即摘除） | 同上 |
| `AgentExecutionOrphanTimeout` | 2min | 同上 |

TTL 在流创建时设一次，由 runner 已有心跳 tick（续租 interval）搭车 `EXPIRE`，订阅者 attach 时也续一次。**不新增定时器。**

### 6.8 事件清单与命名

后端今天只给 `approval_required` 起名，其余靠前端嗅探 data 字段（`onExecutionId` 找 `execution_id`、`onDone` 找 `done`）。

**决定给所有事件起名**（`meta` / `token` / `delegate` / `done` / `error` / `approval_required` / `reset`）。**加 `event:` 行是纯增量**——今天的前端忽略它，行为完全不变，因此排在 PR 3 而不阻塞前两片。前端是否改按名分发是独立决策。

## 7. 生命周期

### 7.1 控制通道

`agent:ctrl:{tenant}:{execution_id}`，**只有 runner 订阅**（每 runner 一条连接，不是每订阅者一条）。消息两类：

| 消息 | 发送方 | 时机 |
|---|---|---|
| `{"type":"viewer"}` | 订阅者 | 复用已有 5s 心跳 tick（`SSEHeartbeatInterval`）搭车发送 |
| `{"type":"cancel"}` | controller | 用户点「停止生成」 |

runner 维护 `map[viewerID]lastSeen`。**viewerID 由订阅者为每条 SSE 连接生成唯一的随机串，不接受客户端提供**——客户端可控的 ID 可以被伪造，用同一个 ID 反复续期就能永久压制孤儿计时。本地判断：

- **孤儿计时**：`len(活跃 viewer) == 0` 持续 `AgentExecutionOrphanTimeout` → 自取消。viewer 15s 无心跳即摘除——**崩溃/断网自然过期，runner 不需要查询 Redis**
- **取消**：收到 `cancel` → 立刻 cancel run

收益三合一：**零额外连接**（订阅者复用已有心跳 tick，runner 复用已有订阅连接）、**零 SCAN / 零查询**、**取消延迟 <1s**（不像轮询租约要等 10s）。

runner **必须在开跑前就订阅好控制通道**，否则会漏掉早到的 viewer 消息。**实现注意**：go-redis 的 `Subscribe` 独占一条连接，Pub/Sub 与 Stream 命令不能共用同一个 `*PubSub` 句柄，控制通道要用独立的 `Client` 实例。

**安全边界**：Pub/Sub 通道**不承载鉴权**——发布前必须在 HTTP 层校验 actor 对该 execution 有所有权。

### 7.2 重连决策树

```
带 execution_id 的 SSE 请求
  │
  ├─ 租户限定的 checkpoint 查询（查不到 → 404，不区分不存在/不属于你）
  │
  ├─ 租约有效 → TAIL
  │    只订阅 agent:stream:{t}:{eid}:{gen}，不走 run
  │    （无需区分持有者是不是本进程 —— D1 的直接收益）
  │
  ├─ 租约过期/无主 → CLAIM（CAS expect run_generation）
  │    ├─ 抢到 → gen+1 → 从 checkpoint 恢复 → 跑 → 写新 key
  │    └─ 没抢到 → TAIL（读赢家的流）
  │
  └─ 无 execution_id → NEW：gen=1，建流，开跑
```

### 7.3 停止生成

```
前端 停止 → POST /agents/:id/executions/:eid/stop → 任意 pod
              ├─ HTTP 层鉴权（actor 有所有权）
              └─ PUBLISH agent:ctrl:{t}:{eid} {"type":"cancel"}
runner 收到 → cancel execCtx → run 退出
              → checkpoint 保留（retainRunningError 认 context.Canceled，agent.go:2533-2544）
              → 写终态帧到流
订阅者收到 → 渲染「已停止」，断流
```

**`status` 不动 CHECK 约束**：现有约束是 `('running','paused','waiting_approval','completed','failed','expired')`，**没有 `cancelled`**。复用 `failed` + `resume_reason='cancelled'`——`resume_reason` 就是为这种区分设计的，且避免动 CHECK 约束（改 CHECK 需处理存量租户，属破坏性迁移，须单独审查）。

### 7.4 关闭顺序

| 场景 | 行为 |
|---|---|
| 订阅者断开 | 只注销 viewer 与关 writer；`WriteUntilClosed` 排水后退出 |
| pod SIGTERM | 停止接收新执行 → 取消所有 run → `WaitGroup.Wait()`。run 中断时 `retainRunningError` 保留 checkpoint（已通），新 pod 可续 |
| Redis 掉线 | XADD 超时即丢；订阅侧读失败 → 发 reset 帧；**不阻塞 run** |

`runHandle` 照抄 `workflow/runtime.go:36-57` 形状：`close(done)` 后 `cancel()`，旧实例原子替换。

### 7.5 F5 完整时序

```
t0  用户提问 → NEW → runner 起 → agent:stream:{t}:{eid}:1 开始
t1  前端渲染 N 个字符，内存持有 cursor = 1726...-37
t2  F5。HTTP 断开 → viewer 注销（不通知 runner）
    runner 继续跑、继续 XADD；viewer 集合要 2 分钟后才可能为空
t3  页面加载 → useChatPage 调 getActiveExecution → running
t4  重连 SSE，带 execution_id + generation=1（不带 cursor —— React 状态已丢）
t5  服务端：genClient == genNow，但无 cursor → 从流的开头全量回放
t6  前端以 resume 模式渲染（不清 content，本来也是空的）→ 毫秒级补全
t7  XREAD BLOCK 接上实时 → 继续流式
```

**t2→t6 之间没有任何一次 LLM 重新调用。** 这就是「接着上次继续输出」。

## 8. 前端改动

### 8.1 确认过的既有事实

- `consumeSSE`（`client.ts:225-229`）**已在解析 `id:` / `event:`**，只是 `streamApiEvents:302` 用 `onEvent(event.data)` 丢弃了
- `streamApiEvents` 的生产调用点**只有 `agent.api.ts:220` 一处**，其余全是测试
- `onToken` 的守卫是 `if (stateRef.current.ctrl !== ctrl) return`；`agent.api.ts` 内部重连**复用同一个 `ctrl`**，守卫天然放行

### 8.2 改动清单

| # | 改动 | 位置 | 切片 |
|---|---|---|---|
| F1 | `streamApiEvents` 透出完整 envelope：`onEvent(event)`，签名与 `streamApiGet` 对齐 | `client.ts:302` + `agent.api.ts:220` | PR 2 |
| F2 | `startStream` 增加显式模式 `'fresh' \| 'resume'`，**`resume` 不清 `content`** | `ChatStreamContext.tsx:124` | PR 2 |
| F3 | sessionStorage 从 `executionId: string` 改为 `{executionId, generation}`，带旧格式迁移 | `agent.api.ts:141-166` | PR 2 |
| F4 | 重连/续接 payload 补 `generation`；`last_event_id` 从**内存**取，无则不传 | `agent.api.ts` payload 构造 | PR 2 |
| F5 | 新增 `onReset` 回调 → 清 `content`、更新 `generation` | `ChatStreamContext` + `useChatPage` | PR 2 |
| F6 | 停止按钮接线：`cancelStream` → 新 API `stopAgentExecution` | `ChatStreamContext.tsx:200-209` + 输入区 UI | **PR 1** |

**F2 的模式必须显式传，不能靠 `payload.execution_id` 推断**——审批续跑（`doApprovalResume`）也带 execution_id，但它**应该清空**：`waiting_approval` 的 checkpoint messages snapshot 为空，恢复落回 base，从 chat 历史 + 本轮 query 全量重跑（H1 语义）。三个入口清空策略不同：

| 入口 | content | 理由 |
|---|---|---|
| `doFreshResume`（F5 续跑） | **不清** | 服务端全量回放补上 |
| `doApprovalResume`（审批后） | **清** | 服务端从 base 全量重跑 |
| 新问题 | 清 | 新 execution |

### 8.3 终态对齐已是对的

`useChatPage.ts:593`：

```ts
const finalContent = streamResult.output || accumulatedContent;
```

`done` 走**覆盖语义**，最终落库内容取服务端 `output`。所以回放期间累积的 `content` 不会成为最终答案。**「文本重复」只在流式过程中可见，不影响终态。**

### 8.4 多标签页

两个 tab 各持独立 `ChatStreamContext` 与独立 `ctrl`，都 attach 到同一条流，各自从零渲染（都是「F5 场景」）。互不干扰，各自都有完整答案。

## 9. 安全

| 项 | 措施 |
|---|---|
| 跨租户流读取 | key 过 `tenantnaming`；订阅前租户限定查询；查不到 404 且不区分「不存在/不属于你」 |
| 停止端点授权 | actor 必须对该 execution 有所有权，HTTP 层校验后才 PUBLISH |
| Pub/Sub 通道 | **不承载鉴权**；`execution_id` 出现在通道名中，但通道不外网可达 |
| 凭据 | 游标走 POST body，不进 URL；流不承载任何 token/cookie |

## 10. 降级与失败路径

| 故障 | 行为 |
|---|---|
| Redis 不可用 | XADD 超时即丢（**不阻塞 LLM**）→ 续传退化为「重新生成」，**run 本身不受影响** |
| 流过期/裁剪 | cursor 早于现存最老 entry → 发 reset（`stream_lost`）→ 前端清空重渲染 |
| 续租失败 | runner **自取消**，不继续烧 token |
| 无订阅者超时 | 取消 run，checkpoint 保留可续 |
| 老客户端（无 `generation`） | 视为 0 ≠ genNow → reset + 全量回放（对 F5 场景恰好正确） |

## 11. 测试与验收

### 11.1 分层

| 层 | 测什么 | 依赖 |
|---|---|---|
| 单元 | `StreamStore`：Append / Range / Tail / TTL / MAXLEN 裁剪 | **miniredis**（v2.38.0 已带 `stream.go` + `cmd_stream.go`；go-redis v9.7.3 全套 Stream API） |
| 单元 | 租约 Claim / Renew / Fence 的 CAS 语义 | 照 `workflow` store 测试形状 |
| 单元 | **游标解析 + gen 比对 + reset 判定（抽成纯函数）** | 无 |
| 单元 | `sse_writer` 的 `id:` 行 | 扩 `sse_writer_test.go` |
| 单元 | 订阅侧三分支（增量 / 全量 / reset） | mock `StreamStore` |
| 前端 | envelope 透出、`resume` 模式不清 content | 扩 `client.test.ts`、`agent.api.test.ts` |
| **跨实例** | 见 §11.2 | PG + Redis + 两个实例 |
| E2E | F5 后不重生成（无头 Chromium） | Playwright |

### 11.2 跨实例测试是承重墙

`make test-verify-before-pr` 的 E2E 是**单实例**的，覆盖不到「重连落到另一个 pod」——而这正是整个设计的核心。必须另起 Go 集成测试：同一套 PG + Redis，起**两个** `AgentService` 实例，LLM 用假 provider。

**硬性断言**：

1. A 跑期间，B 用同一 `execution_id` attach → **B 收到 token**
2. **LLM 调用次数 == 1** —— 「没有重新生成」的直接证据，本次改造的北极星
3. A 的 HTTP 断开后 run **继续跑**（未被 cancel）
4. A 断开、B attach 之后，runner 的 viewer 集合仍非空 → 孤儿计时**不**启动

**本地即可运行**：`make infra-up` 起 PG/Redis，跑两个 `cmd/server`（不同端口、同一套依赖）。符合「E2E 优先本地 Docker」。

前置条件：§D5 的可注入时长——否则每个跨实例测试都要跑 2 分钟量级。

### 11.3 失败路径必须显式覆盖（risk-regression-guard 规则 7）

| 场景 | 断言 |
|---|---|
| Redis 不可用 | run **正常完成**；订阅侧降级；不阻塞 LLM |
| 续租失败 | runner **自取消**，不继续烧 token |
| 两个 runner 并存窗口 | 流**不交错**（gen 独立 key 的物理保证） |
| 租约过期抢跑 | 只有一个 runner 活；败者 TAIL |
| 跨租户 `execution_id` | 404，不区分「不存在/不属于你」 |
| 无订阅者 2 分钟 | run 被取消，checkpoint 保留 |

### 11.4 契约（已修正：不改 proto）

**本节初稿的判断有误**，实施前取证推翻，记录于此避免重犯：

`proto/agent/agent.proto:32` 的 `ExecuteAgentRequest` **不是**流式请求体的事实源。它的生成物 `gen.ExecuteAgentRequest` **零生产引用**（`grep -rn "gen\.ExecuteAgentRequest" --include='*.go' .` 无命中），是死代码。agent execute 路由实际绑定的请求体是**手写**结构体 `api/http/handler/agent_dto.go:96`，绑定点在 `agent_exec_handler.go:34` / `:119` 的 `c.ShouldBindJSON`。

因此 PR 1 的契约改动是：

- `generation`、`last_event_id` **加到手写结构体**，与既有的 `conversation_id` / `execution_id` / `options` 同属 wire-only 字段；前端按 `model/agent.ts:314-322` 既有方式手写声明。
- **不改 proto。** 改 proto 反而有额外成本：`cmd/protoc-gen-ginstruct/testdata/production-snapshots/agent.proto.golden.go` **入库**且被 `TestProductionProtoSnapshots` 逐字节比对，改 proto 必须跑 `go test ./cmd/protoc-gen-ginstruct -run TestProductionProtoSnapshots -update` 重写基线。为一个无人消费的 message 付字节级快照成本不划算。
- `scripts/quality/dto-residue-guard.sh` **不构成阻碍**：它只查「`api/http/dto/` 顶层无手写 `.go`」「无旧 dto 包 import」「`web/src/modules` 不重现 4 个已迁移类型名」，**不做字段级双向一致性校验**。

**必须如实记录的护栏现状**：agent execute 的 golden（`post_agents__id_execute_stream.golden.json`）**只有一条 401 用例**，断言只看 `want_status` / `want_body`，新增请求字段**不会**让 `TestContracts` 变红。即这条路径上 CI 的护栏是「生成器不漂移」，**不是**「字段契约」。所以 stop 端点必须显式补一条有内容断言的 golden（PR 1 任务 13），否则新端点在契约层是裸的。

### 11.5 验收门槛

R3 → `make test-verify-before-pr` + soak（`STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all`）；因命中 `eval-touched`，还需 `scripts/quality/run-eval-checks.sh`。**必须由 `stratum-e2e-tester` 执行系统验收，不得绕过 skill 手工拼装。**

## 12. 落地切片

**不能按「先租约后日志」拆**：租约有意义的前提是「没抢到租约的一方有别的路可走」，而唯一的另一条路就是读共享日志。只上租约不上日志 → 跨 pod 重连时 B 看到租约在 A 手上 → B 只能拒绝 → **前端卡死**（「把 rerun 变成 stuck」的坑换个形式）。

**一个意外的好消息**：服务端对「无游标请求」的默认分支正是**全量回放当前 generation**，而前端今天发的就是无游标请求。所以 **PR 1 不加任何前端配合就已经修好 F5**。

| PR | 内容 | 效果 | 前端 |
|---|---|---|---|
| **1**<br>后端续传内核 | `tenantnaming/redis.go`、`StreamStore`、`lease_expires_at` 列 + 租约 CAS、去掉 `cancel()`、订阅侧三分支、控制通道、孤儿超时、`sse_writer` 的 `id:` 行、**wire-only 请求字段**（不改 proto）、停止端点 + **停止按钮 UI** | F5 无缝（回放而非重生成）；双跑修复；停止可用 | 只加停止按钮（F6） |
| **2**<br>前端游标 | F1–F5 | 自动重连从「重复」→「无缝」 | 纯前端，**零后端改动** |
| **3**<br>事件命名 | 后端给所有事件起名（§6.8）；前端可选切按名分发 | 协议可读性 | 可选 |

**停止按钮必须在 PR 1**：去掉 `cancel()` 之后「断连即停」的保护没了，若 PR 1 上线时无手动停止入口，用户开了长回答只能干等 2 分钟——不可接受的体验倒退。

**PR 1 不修故障 2**（§2.3 的抖动重复）：前端仍不发游标 → 服务端仍是全量回放 → 前端仍是非空 `content` 追加。行为与今天相同，**不变差也不变好**；修复在 PR 2。

每片均为 R3，各自走完整门槛。**实施计划先覆盖 PR 1**；PR 2 / PR 3 待 PR 1 合入后另行出计划（PR 2 依赖 PR 1 已上线的 wire 协议）。

## 13. 明确不做（YAGNI）

- **前端持久化已渲染文本**（跨 F5 用游标续接）—— 全量回放已是毫秒级，不值得为省掉一次重绘引入一整类对齐 bug
- **流内周期性快照**（裁剪后仍能恢复完整文本）—— MAXLEN 20000 对正常答案有 3–5 倍余量，超限走 `stream_lost` reset
- **按事件名分发的完整前端重写** —— 后端起名是纯增量，前端可后续再切
- **`status` 增加 `cancelled` 枚举** —— 复用 `resume_reason`，避免动 CHECK 约束
- **session affinity / sticky** —— 不把正确性绑在部署配置上

## 14. 变更清单（文件级）

### 新增

- `pkg/storage/tenantnaming/redis.go`（`TenantKey`，照 `nats.go` 的 `TenantSubject` 形状，fail closed）
- `pkg/storage/redis/stream.go` + `stream_test.go`（通用 Redis Streams 薄封装）
- `internal/agent/domain/port/agent_stream.go`（`AgentStreamStore` / `AgentControlBus` / `ExecutionLeaseRepo`；按 DDD 分层由消费方定义）
- `internal/agent/infrastructure/stream/agent_stream_store.go`（端口实现，内含租户命名空间）
- `internal/agent/infrastructure/stream/control_bus.go`（Pub/Sub；**独立 `*goredis.Client`**）
- `internal/agent/application/agent_stream_session.go`（订阅会话、viewer 注册、孤儿计时、纯函数 `PlanStream`）
- agent 流相关的常量（`pkg/constants/agent.go` 追加）

### 修改

- `pkg/storage/postgres/tenant_schema.sql`（`lease_expires_at` 列，`IF NOT EXISTS` + 覆盖历史租户）
- `api/http/handler/agent_exec_handler.go`（去 `cancel()`、订阅化、控制通道、孤儿超时、stop 端点）
- `api/http/handler/agent_dto.go`（wire-only `generation` / `last_event_id`；**不改 proto**，见 §11.4）
- `api/http/handler/sse_writer.go`（`id:` 行）
- `api/http/router.go`（stop 路由）
- `internal/agent/application/agent_execution.go`（`ExecuteStream` 签名：移除 `tokenCb`，改为返回 `StreamRun`；`ExecMeta` 加 `Generation` / `LastEventID`）
- `internal/agent/infrastructure/persistence/checkpoint_store.go`（Stamp / Claim / Renew / Release 租约 SQL）
- `api/wiring/`（StreamStore 装配、可注入时长、关闭注册）
- `web/src/services/client.ts`、`web/src/modules/agent/api/agent.api.ts`、`web/src/modules/agent/hooks/ChatStreamContext.tsx`、`useChatPage.ts`、输入区组件

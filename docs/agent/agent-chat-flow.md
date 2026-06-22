# Agent 会话完整流程图

以一次 agent chat 的 query 输入为切入点，从前端用户键入消息，经由 SSE 流式接口、AgentService 编排、ReAct StateGraph 调度，到 LLM/工具调用、消息持久化与流式回传 token，绘制端到端业务流程。

---

## 1. 鸟瞰：一次会话的端到端

```mermaid
flowchart LR
    subgraph FE["前端 web/"]
        Page[ChatPage]
        Hook[useChatPage]
        Stream[ChatStreamContext]
        Api[executeAgentStream]
    end

    subgraph Edge["传输层 api/http"]
        Router[Gin Router]
        MW[Middleware<br/>Auth · Tenant · Trace]
        Handler[AgentHandler.ExecuteAgentStream]
    end

    subgraph App["应用层 internal/agent/application"]
        Svc[AgentService.ExecuteStream]
        Base[BaseAgent.Execute]
        Graph[ReAct StateGraph]
    end

    subgraph Cap["能力层 capgateway"]
        Gw[CapabilityGateway.Route]
        LLM[LLM Provider<br/>Qwen / Zhipu / OpenAI compat]
        Skill[Skill Engine]
        RAG[RAG Service]
        Mem[Memory Injector / Recall]
    end

    subgraph Store["存储层"]
        Chat[(ChatStore)]
        Exec[(ExecutionStore)]
        Outbox[(Memory Outbox)]
    end

    Page --> Hook --> Stream --> Api
    Api -- POST /agents/:id/execute/stream --> Router
    Router --> MW --> Handler
    Handler --> Svc --> Base --> Graph
    Graph --> Gw
    Gw --> LLM
    Gw --> Skill
    Base --> RAG
    Base --> Mem
    Base --> Chat
    Svc --> Exec
    Base -- 新消息事件 --> Outbox
    LLM -- token 回调 --> Handler
    Handler -- SSE data: {token} --> Api -- onToken --> Stream
```

**关键点**

- 前端用 `fetch` + `ReadableStream` 解析 SSE，不走 axios
- HTTP 边界做的事只有：解析 → 鉴权/租户 → 调用 Service → 把 token 回调写成 SSE 帧
- `AgentService` 是编排门面；`BaseAgent.Execute` 是真正的执行体；ReAct 循环在 `agent/application/graph` 包里
- Token 流通过 `WithTokenCallback` 注入到 ReAct LLM 节点；最终答案那一轮的每个 token 都触发回调

---

## 2. 前端：从输入框到流式渲染

```mermaid
sequenceDiagram
    autonumber
    participant U as 用户
    participant Page as ChatPage
    participant Hook as useChatPage
    participant Stream as ChatStreamContext
    participant Api as executeAgentStream
    participant BE as Backend SSE

    U->>Page: 输入 query, 点击发送
    Page->>Hook: handleSend(query)

    Note over Hook: 守卫: 检查是否选中 agent/conversation<br/>不再阻塞流式中途发新消息

    alt 已有流式进行中
        Hook->>Stream: cancelStream()
        Note over Stream: 上一条消息打 interrupted 标记
        Hook->>Hook: 在 messages 末尾追加 user 消息
    else 全新会话
        Hook->>Hook: 追加 user 消息 + 占位 assistant 消息
    end

    Hook->>Stream: startStream(agentId, payload)
    Stream->>Stream: 旧 ctrl.abort() · 重置 stateRef
    Stream->>Api: executeAgentStream(id, payload, callbacks)

    Api->>BE: fetch POST /agents/:id/execute/stream<br/>Authorization: Bearer ...
    activate BE

    BE-->>Api: ": heartbeat\n\n" (首字节)
    loop 每个 SSE chunk
        BE-->>Api: data: {"token":"..."}
        Api->>Stream: onToken(token)
        Stream->>Stream: stateRef.content += token<br/>scheduleNotify (RAF 合批)
        Stream-->>Page: 重渲染流式内容 (~60fps)
    end

    BE-->>Api: data: {"done":true,"output":...,"tokensUsed":...}
    deactivate BE
    Api->>Stream: onDone(data)
    Stream->>Hook: streamDone=true · streamResult=data
    Hook->>Hook: 把流式 content 落盘到 messages[]
    Hook->>BE: GET /conversations/:id/messages (rehydrate)
```

**前端关键设计**

- `ChatStreamContext` 用 `ref` + `requestAnimationFrame` 合批通知，避免每个 token 触发 React 重渲染
- 流式中可发新消息：旧 controller `abort` → 旧消息 UI 标 "已中断" → 立即开始新流
- 刷新/切会话时通过 `getStreamState()` 恢复未完成流的 UI

---

## 3. 后端入口：SSE Handler 的 5 件事

```mermaid
sequenceDiagram
    autonumber
    participant Cli as Client (fetch)
    participant Gin as Gin Router
    participant MW as Middleware Chain
    participant H as AgentHandler.ExecuteAgentStream
    participant Svc as AgentService.ExecuteStream

    Cli->>Gin: POST /agents/:id/execute/stream
    Gin->>MW: 进入中间件链

    Note over MW: AuthMiddleware: JWT RS256 解析<br/>userID 注入 c.Keys
    Note over MW: TenantMiddleware: tenant_id 注入<br/>SET LOCAL search_path
    Note over MW: TraceMiddleware: trace_id 生成

    MW->>H: ctx (with tenant/user/trace)

    H->>H: 解析 JSON body → ExecuteAgentRequest
    H->>H: 写 SSE headers (text/event-stream · keep-alive · X-Accel-Buffering: no)
    H->>H: 写首帧 ": heartbeat\n\n" (击穿代理缓冲)

    H->>H: 构造 tokenCb(token) → SSE write

    H->>Svc: ExecuteStream(clientCtx, id, ExecRequest, ExecMeta, tokenCb)
    Svc-->>H: (execCtx, cancel, run, nil)

    par 取消监听
        H->>H: go: select clientCtx.Done() / execCtx.Done()
    and 心跳
        H->>H: go: ticker @ SSEHeartbeatInterval
    and 主流程
        H->>H: result, _, runErr := run()
    end

    alt runErr != nil
        alt 客户端主动断开
            H-->>Cli: silent return
        else 真实错误
            H-->>Cli: data: {"error":"..."}
        end
    else 成功
        H-->>Cli: data: {"done":true,"output":...,"steps":...,"tokensUsed":...}
    end
```

**关键约束**

- `clientCtx` 是 gin 请求 context，客户端断连即取消
- `execCtx` 是 `context.WithoutCancel(streamCtx)` + `WithTimeout(AgentExecTimeout)`，**不会**因为 `Stream`/`MemoryInjector` 调用回流被误取消
- 取消监听 goroutine 是双向的：客户端断 → cancel exec；exec 自然结束 → goroutine 退出

---

## 4. AgentService：装配与编排

```mermaid
flowchart TB
    Start([ExecuteStream]) --> Lookup[Registry.Get agentID]
    Lookup -- not found --> Err1[/return ErrNotFound/]
    Lookup -- found --> Assemble[assembleOptions]

    subgraph AS["assembleOptions"]
        direction TB
        AS1[Resolve TenantCapability<br/>per-tenant capGW + apiKeys]
        AS2{Stream meta?}
        AS3[InjectCompleter into ctx]
        AS4[a.SetCapGateway capGW]
        AS5[attachChatStore a]
        AS6[buildExtraTools<br/>MCP servers + Allowed Skills]
        AS7[append RAGSearchFn closure]
        AS8[append: TenantID·TraceID·UserID·ConversationID·HistoryWindow]
        AS1 --> AS2
        AS2 -- yes --> AS3
        AS2 -- no --> AS4
        AS3 --> AS4
        AS4 --> AS5 --> AS6 --> AS7 --> AS8
    end

    Assemble --> WithCb[append WithTokenCallback]
    WithCb --> Wrap[context.WithoutCancel + WithTimeout AgentExecTimeout]
    Wrap --> RunFn[/构造 run 闭包/]

    RunFn -.返回 execCtx, cancel, run.- Caller([Handler])
    Caller -.调用 run.-> Inside[run start = time.Now]
    Inside --> Exec[a.Execute execCtx, query, options...]
    Exec --> Record[recordExecution → ExecutionStore]
    Record --> Out([返回 result, durationMs, err])
```

**装配顺序的语义**

| 顺序 | 作用 | 失败影响 |
|---|---|---|
| Registry.Get | 加载 Agent 聚合 | 直接 404 |
| TenantResolver.Resolve | 拿到该租户的 LLM 完成器 + apiKey | 该 Agent 无可用模型 |
| InjectCompleter | 把 streaming completer 塞进 ctx | RAG / 工具内嵌 LLM 调用走流式 |
| attachChatStore | 让 BaseAgent 能读写历史 | 历史记录退化为单轮 |
| buildExtraTools | MCP 工具 + Allowed Skills 转 ToolDefinition | 工具调用将报 not found |

---

## 5. BaseAgent.Execute：会话级业务流程

```mermaid
flowchart TB
    Start([BaseAgent.Execute]) --> Snap[加锁快照<br/>id·type·systemPrompt·llmModel·capGW·chatStore·workspace 等]
    Snap --> MemInj{MemoryInjector 配置?}
    MemInj -- 是 --> BC[BuildContext<br/>注入 episodic/semantic memory]
    MemInj -- 否 --> SkipMem
    BC --> SkipMem
    SkipMem --> Log[zap.Info: agent execution started]
    Log --> Hist{ChatStore + ConvID?}
    Hist -- 是 --> Load[ListMessages 加载短期历史]
    Hist -- 否 --> Skip
    Load --> Skip

    Skip --> Type{agentType}
    Type -- ReAct --> ReAct
    Type -- CoT --> CoT[简化思维链 stub]
    Type -- 其他 --> Stub[未实现, 返回错误]

    subgraph ReAct["ReAct 分支"]
        direction TB
        R1[BuildReActGraph capGW]
        R2[BuildContextMessages<br/>system + memCtx + history + 当前 query<br/>maxTokens 截断]
        R3[组装 AvailableTools]
        R3a[添加 stratum_search_knowledge<br/>workspaces enum + query + top_k]
        R3b[添加 stratum_recall_memory]
        R3c[mergeTools extra MCP/Skill]
        R4[构建 ReActState<br/>包含 OnToken=cfg.TokenCallback]
        R5[execCtx WithTimeout cfg.Timeout<br/>注入 trace/tenant 到 reqctx]
        R6[cg.Invoke execCtx, initState, MaxSteps]
        R1 --> R2 --> R3 --> R3a --> R3b --> R3c --> R4 --> R5 --> R6
    end

    ReAct --> Result[填充 result.Output/Steps/TokensUsed/ToolCalls]
    Result --> Persist{ChatStore + execErr==nil?}
    Persist -- 是 --> SaveU[保存 user 消息]
    SaveU --> SaveA[保存 assistant 消息]
    Persist -- 否 --> SkipP
    SaveA --> SkipP[Outbox 发布事件]
    SkipP --> Done([返回 AgentResult])
```

**消息装配规则（`BuildContextMessages`）**

```text
[system] systemPrompt + memCtx
[history…] 倒数 N 条 (HistoryWindow)
[user] 当前 query
按 maxContextTokens 自尾向头丢弃溢出
```

---

## 6. ReAct StateGraph：核心循环

```mermaid
stateDiagram-v2
    [*] --> LLM: SetEntryPoint
    LLM --> Tool: assistant has tool_calls
    LLM --> [*]: assistant 直接答 (END)
    Tool --> LLM: 工具结果回填 messages
    Tool --> [*]: ctx canceled / max steps

    note right of LLM
        makeLLMNode:
        capGW.Route(CapLLM)
        TokenStream=OnToken
        重试: DefaultRetry
        累加 Steps + TotalTokens
        日志 react.llm
    end note

    note right of Tool
        makeToolNode:
        分发到 search_knowledge / recall_memory / skill
        日志 react.tool
        appended Role=tool 到 Messages
    end note
```

```mermaid
sequenceDiagram
    autonumber
    participant Inv as CompiledGraph.Invoke
    participant LLM as LLM Node
    participant Cap as CapabilityGateway
    participant Tool as Tool Node
    participant RAG as RAG Service
    participant Mem as Memory Recall
    participant Skill as Skill Engine
    participant Cb as tokenCb (SSE)

    loop until END or MaxSteps
        Inv->>LLM: enter (state)
        LLM->>Cap: Route(CapLLM, msgs+tools, TokenStream=OnToken)
        Cap-->>Cb: OnToken(token) (持续回调)
        Cap-->>LLM: CapabilityResponse{Content, ToolCalls, Usage}
        LLM->>LLM: state.Steps++ · TotalTokens+=usage.Total

        alt no tool_calls (final answer)
            LLM->>LLM: state.Output=resp.Content<br/>append assistant message
            LLM-->>Inv: 状态: 进入 END 路径
        else has tool_calls
            LLM->>LLM: append assistant{ToolCalls}
            LLM->>Tool: 进入工具节点
            loop 每个 tool_call
                alt stratum_search_knowledge
                    Tool->>RAG: SearchKnowledge(workspaces, query, topK)
                    RAG-->>Tool: 文档片段 + 引用
                else stratum_recall_memory
                    Tool->>Mem: RecallMemoryFn(input)
                    Mem-->>Tool: 历史记忆摘要
                else 其他 (skill/MCP)
                    Tool->>Cap: Route(CapSkill, SkillID, args)
                    Cap->>Skill: provider.Execute
                    Skill-->>Cap: output
                    Cap-->>Tool: toolResp
                end
                Tool->>Tool: append role=tool message
            end
            Tool-->>Inv: 回到 LLM 节点 (再循环)
        end
    end

    Inv-->>LLM: 退出循环 (返回 finalState)
```

**循环边界**

- `MaxSteps`：每进入一次 LLM 节点 +1
- `cfg.Timeout` (默认 120s)：通过 `execCtx` 控制；ReAct 内每步开头检查 `ctx.Done()`
- `reactLLMTimeout = 60s`：单次 LLM 调用超时（独立于 exec 总超时）
- 重试：`DefaultRetry` 策略包裹每次 `capGW.Route`，瞬态错误指数退避

---

## 7. Token 流：从 LLM Provider 到浏览器

```mermaid
flowchart LR
    Llm[LLM Provider<br/>OpenAI compat SSE chunk] -->|chunk| Compl[Per-Tenant Completer]
    Compl -->|TokenStream callback| GwLLM[CapabilityGateway LLM 适配]
    GwLLM -->|OnToken| ReactState[ReActState.OnToken]
    ReactState -->|cfg.TokenCallback| BaseAgent
    BaseAgent --> SvcCb[tokenCb 闭包]
    SvcCb --> SSE[Handler tokenCb<br/>fmt.Fprintf data: token]
    SSE --> Wire[(网络 SSE 帧)]
    Wire --> FetchReader[fetch ReadableStream]
    FetchReader --> Parse[split '\n\n' · slice 'data:']
    Parse --> Stream[ChatStreamContext.onToken]
    Stream --> Raf[scheduleNotify RAF]
    Raf --> React[React Re-render @ ~60fps]
    React --> User[用户看到流式文字]
```

**只有"最终答案"那一轮的 token 会被用户看到**

工具决策轮的 LLM 输出是 tool_call JSON，content 几乎为空——前端不会看到任何 token；用户只在 LLM 决定不再调用工具、直接生成自然语言时才看到流式输出。

---

## 8. 持久化与可观测性

```mermaid
flowchart LR
    subgraph Persist["每次成功执行落盘"]
        UserMsg[chat_messages: user]
        AsstMsg[chat_messages: assistant]
        Exec[(executions: 状态/输入预览/输出预览/tokens/duration)]
    end

    subgraph Outbox["异步记忆管道 (NATS JetStream)"]
        OB[memory_outbox] --> Embedder
        Embedder --> Enricher
        Enricher --> Mil[(Milvus)]
        Enricher --> Neo[(Neo4j)]
    end

    subgraph Logs["Zap 结构化日志"]
        L1[agent execution started]
        L2[react.llm step/tokens/latency]
        L3[react.tool tool_name/latency]
        L4[llm.complete model/tokens]
    end

    BaseAgent --> UserMsg & AsstMsg
    AgentService --> Exec
    BaseAgent -.事件.-> OB
    BaseAgent -.zap.-> L1
    ReactGraph -.zap.-> L2 & L3
    LLMProvider -.zap.-> L4
```

| 日志事件 | 关键字段 |
|---|---|
| `agent execution started` | agent_id, trace_id, conversation_id, type, input |
| `react.llm` | trace_id, tenant_id, model, step, prompt/completion/total_tokens, latency_ms, has_tool_calls |
| `react.tool` | trace_id, tool_name, latency_ms |
| `llm.complete` | model, provider, prompt_tokens, completion_tokens, latency_ms |

---

## 9. 错误与中断

```mermaid
stateDiagram-v2
    [*] --> Streaming
    Streaming --> ClientAbort: fetch AbortController
    Streaming --> ServerTimeout: AgentExecTimeout
    Streaming --> LLMError: capGW.Route 失败 (已重试)
    Streaming --> ToolError: skill/RAG/recall 失败
    Streaming --> Success: END 节点

    ClientAbort --> Cleanup: handler goroutine select clientCtx.Done<br/>调用 cancel()
    Cleanup --> [*]: 静默返回 (不写 SSE error)

    ServerTimeout --> WriteErr: data: {"error":"..."}
    LLMError --> WriteErr
    ToolError --> ContinueLoop: 把 error 当 tool 输出回填消息<br/>下一轮 LLM 决定如何处理
    ContinueLoop --> Streaming

    Success --> WriteDone: data: {"done":true,...}
    WriteErr --> [*]
    WriteDone --> [*]
```

**前端断点续显**

- 用户切走再切回会话：`useChatPage` 通过 `getStreamState()` 检测同 `conversationId` 的活跃流，把累计 content 重新挂到占位 message 上
- 中途发新消息：旧 `ctrl.abort()` → 后端 `clientCtx` 被取消 → execCtx 被 cancel goroutine 取消 → ReAct 在下一个 `ctx.Done()` 检查点退出 → 数据库 **不**写入这条未完成的 assistant message（因为 `execErr != nil`）

---

## 10. 关键文件索引

| 关注点 | 路径 |
|---|---|
| 前端流式 hook | `web/src/modules/agent/hooks/ChatStreamContext.tsx` |
| 前端聊天页 hook | `web/src/modules/agent/hooks/useChatPage.ts` |
| 前端 SSE fetch | `web/src/modules/agent/api/agent.api.ts:executeAgentStream` |
| HTTP Handler | `api/http/handler/agent_exec_handler.go` |
| AgentService 编排 | `internal/agent/application/agent_service.go` |
| BaseAgent 执行 | `internal/agent/application/agent.go:Execute (L183)` |
| ReAct 图定义 | `internal/agent/application/graph/react.go` |
| Graph Invoke 引擎 | `internal/agent/application/graph/graph.go` |
| 上下文消息装配 | `internal/agent/application/agent.go:BuildContextMessages` |
| ChatStore | `internal/agent/infrastructure/chatstore/` |
| ExecutionStore | `internal/agent/infrastructure/execstore/` |
| 容量网关路由 | `internal/agent/domain/port/capability.go` + `infrastructure/capgateway/` |
| 记忆注入 | `internal/memory/...` (consumer-side port at `internal/agent/domain/port/memory.go`) |

---

## 元约束速查（来自 CLAUDE.md）

- AI 不做控制逻辑：ReAct 循环判定 / 工具路由 / 重试退避全部硬编码在 `react.go` + `RetryFn`
- handler ≤15 行/方法：`ExecuteAgentStream` 实测 ~60 行（SSE 模板代码无可压缩，已是最简）
- 跨 ctx 通过消费方 port：`port.CapabilityGateway` 定义在 agent 的 `domain/port/`，由 capgateway 实现
- 多租户：`SET LOCAL search_path` 在 TenantMiddleware 注入；execCtx 通过 `context.WithoutCancel` 隔离，但 `tenantdb` 已在 ctx 中绑定

---

## 11. 异步记忆写入管道（详细）

ChatStore 落盘的 `chat_messages` 只是"短期记忆"（按 conversation_id 直读）。**长期记忆**走 outbox → JetStream → embedder → enricher 三段式异步管道，最终落到 Milvus 向量库 + PostgreSQL `memory_entries` + `entities` 表。

### 11.1 三段式管道总览

```mermaid
flowchart LR
    subgraph Tx["事务边界 (BaseAgent.Execute)"]
        Save[保存 chat_messages]
        OB[INSERT memory_outbox<br/>同事务原子提交]
    end

    subgraph Stage1["Stage 1: Outbox Poller (每 N 秒轮询全租户)"]
        Pol[轮询每个 tenant_xxx schema<br/>FOR UPDATE SKIP LOCKED]
        Pub1[NATS Publish<br/>memory.raw.<tenant_id>]
        Del[DELETE FROM memory_outbox]
        Pol --> Pub1 --> Del
    end

    subgraph Stage2["Stage 2: Embedder Worker (N 个并发)"]
        Fetch1[JetStream Fetch 1 msg]
        Resolve1[per-tenant EmbedClient resolver]
        Embed[EmbedVector text → []float32]
        Mil[Milvus.Upsert<br/>tenantID + userID + msgID + vector + metadata]
        Pub2[NATS Publish<br/>memory.enriched.<tenant_id>]
        Ack1[msg.Ack]
        Fetch1 --> Resolve1 --> Embed --> Mil --> Pub2 --> Ack1
    end

    subgraph Stage3["Stage 3: Enricher Worker (N 个并发)"]
        Fetch2[JetStream Fetch 1 msg]
        Resolve2[per-tenant LLMClient resolver]
        Llm[LLM.Complete<br/>抽取实体/关键词/重要度<br/>Temperature=0.1]
        Tx2[BEGIN tx · SET LOCAL search_path]
        Up1[UPSERT memory_entries<br/>type=long_term + importance + keywords]
        Up2[UPSERT entities<br/>GREATEST confidence 合并]
        Commit[COMMIT]
        Ack2[msg.Ack]
        Fetch2 --> Resolve2 --> Llm --> Tx2 --> Up1 --> Up2 --> Commit --> Ack2
    end

    Save --> OB
    OB -.每 N 秒.-> Pol
    Pub1 -. JetStream MEMORY_RAW .-> Fetch1
    Pub2 -. JetStream MEMORY_ENRICHED .-> Fetch2
```

### 11.2 Outbox：原子写入 + 解耦发布

```mermaid
sequenceDiagram
    autonumber
    participant Base as BaseAgent.Execute
    participant CS as ChatStore (同事务)
    participant OB as memory_outbox 表
    participant Poll as OutboxPoller
    participant JS as NATS JetStream

    Base->>CS: BEGIN TX
    CS->>CS: INSERT chat_messages (user)
    CS->>CS: INSERT chat_messages (assistant)
    Note over CS,OB: 同一事务里写消息 + outbox<br/>保证"消息可见 ⟺ 事件可发"
    CS->>OB: INSERT memory_outbox(payload=MemoryRawEvent)
    Base->>CS: COMMIT

    loop 每 PollInterval (默认 ~5s)
        Poll->>Poll: ListTenantSchemas (全租户)
        loop 每个 tenant_xxx
            Poll->>OB: BEGIN TX · SET search_path · SELECT FOR UPDATE SKIP LOCKED LIMIT batch
            Poll->>JS: Publish memory.raw.<tenant_id>
            Poll->>OB: DELETE WHERE id = ANY(ids)
            Poll->>OB: COMMIT
        end
    end
```

**关键设计**

| 设计点 | 含义 |
|---|---|
| 同事务双写 | 业务表 + outbox 必须原子提交，否则消息可见但事件丢失（或反之） |
| `FOR UPDATE SKIP LOCKED` | 多 poller 实例并存时不互相阻塞，"谁锁到谁负责" |
| `SET LOCAL search_path` | 每个 tenant 独立 schema，poller 顺序轮询所有 schema |
| 主题路由 `memory.raw.<tenant_id>` | JetStream 按 tenant 分流，下游可按租户做配额/限流 |
| DELETE 在 publish 之后 | "至少一次"语义；publish 失败回滚事务保留行，下次再发 |

### 11.3 Embedder：向量化 + Milvus Upsert

```mermaid
flowchart TB
    Start([Fetch from MEMORY_RAW]) --> Unmar[UnmarshalRawEvent]
    Unmar -- 解析失败 --> Ack0[Ack 丢弃 · log error]
    Unmar -- ok --> Resolve{embedSvc nil?}
    Resolve -- 否 --> Use[使用全局 embedSvc]
    Resolve -- 是 --> Per[embedResolver ctx, tenantID]
    Per --> Skip{nil?}
    Skip -- 是 --> AckSkip[Ack 跳过 · log warn<br/>该租户未配 embedding]
    Skip -- 否 --> Use
    Use --> Embed[EmbedVector content]
    Embed -- 失败 --> Nak1[Nak 重投递 · maxDeliver 后进 DLQ]
    Embed -- ok --> Up[Milvus.Upsert<br/>tenantID/userID/msgID/vector + metadata]
    Up -- 失败 --> Nak1
    Up -- ok --> Pub[Publish memory.enriched.<tenant_id>]
    Pub -- 失败 --> Nak1
    Pub -- ok --> Ack1[msg.Ack · 计数 success]
```

**Milvus 写入 metadata 字段**

```text
conversation_id · user_id · agent_id · role · content · created_at(RFC3339)
```

向量 ID = `MessageID`（chat_message 主键）；后续 recall 时通过 ID 反查 `memory_entries` 拿到富化结果。

### 11.4 Enricher：LLM 富化 + 关系图谱写入

```mermaid
sequenceDiagram
    autonumber
    participant Worker as EnricherWorker
    participant JS as JetStream
    participant LLM as Per-Tenant LLM
    participant DB as PostgreSQL (tenant schema)

    Worker->>JS: Fetch from MEMORY_ENRICHED
    Worker->>Worker: UnmarshalEnrichedEvent

    Worker->>LLM: Complete(prompt, T=0.1)
    Note right of LLM: prompt 模板要求返回严格 JSON:<br/>{importance, keywords[], entities[], token_estimate}

    LLM-->>Worker: JSON content
    Worker->>Worker: json.Unmarshal → EnrichmentResult

    alt 解析失败
        Worker->>JS: msg.Nak (重试)
    else 解析成功
        Worker->>DB: BEGIN
        Worker->>DB: SET LOCAL search_path = tenant_<id>
        Worker->>DB: UPSERT memory_entries<br/>type=long_term · importance · keywords · token_estimate · enriched_at
        loop 每个抽取出的 entity
            Worker->>DB: UPSERT entities<br/>ON CONFLICT(name,type,user_id)<br/>confidence = GREATEST(old, new)
        end
        Worker->>DB: COMMIT
        Worker->>JS: msg.Ack
    end
```

**重要不变量**

- `memory_entries.id = chat_messages.id = vector_id`（Milvus），三库通过同一 UUID 串联
- `entities` 唯一键 `(name, type, user_id)`：实体属于用户，不属于租户也不属于 conversation
- 富化结果是**只读**事实：用户 UI 不直接展示 importance/keywords，只在 recall 时用做排序/过滤
- LLM 解析失败会一直 Nak，达到 `maxDeliver` 后消息进死信队列；不会污染数据

### 11.5 召回：从 stratum_recall_memory 工具到 LLM

```mermaid
flowchart LR
    Tool[stratum_recall_memory<br/>query 参数] --> RecallFn[BaseAgent.RecallMemoryFn]
    RecallFn --> Inj[MemoryInjector.Recall<br/>InjectionContext: tenant + user + agent + conv]
    Inj --> Query[memory_repo.Search<br/>当前实现: 纯 SQL 搜索 memory_entries]
    Query --> Format[拼装为 markdown 摘要文字]
    Format --> Tooled[作为 role=tool 消息回填 ReActState.Messages]
    Tooled --> NextLLM[下一轮 LLM 决定是否继续工具调用 / 直接答]
```

> 当前 `memory_repo.Search` 采用 PostgreSQL 全文/关键字检索（不是向量召回）；Milvus 向量库已写入但**召回路径暂未启用语义检索**，是已识别的演进点（参见观察 5279）。

### 11.6 失败语义与背压

| 失败位置 | 语义 | 防护 |
|---|---|---|
| outbox 同事务写入 | 业务事务回滚 → 事件不发布 | 业务表与 outbox 强一致 |
| poller publish 失败 | 事务 rollback 保留 outbox 行 | 下次轮询重发 |
| embedder Embed 失败 | Nak → 重新投递 | `maxDeliver` 上限后转 DLQ |
| embedder Milvus 失败 | Nak | 同上；vectorDB 故障时 backlog 累积 |
| enricher LLM 失败 | Nak | 同上；DLQ 由人工 / 巡检处理 |
| 全管道关闭 | `Pipeline.Stop()` 取消 ctx + Wait wg | outbox 行保留，重启后续传 |

---

## 12. 登录、认证与授权

Stratum 的认证体系基于 GitHub OAuth + 双 JWT（access + onboarding）+ 刷新 cookie；授权采用三层（global_role / tenant_role / 资源所有者）+ 租户激活检查。

### 12.1 登录全景：三种结果分支

```mermaid
flowchart TB
    Start([GET /auth/github 浏览器]) --> SetState[生成随机 state<br/>SetCookie oauth_state · 5min]
    SetState --> Redir[302 → github.com/login/oauth/authorize<br/>client_id + redirect_uri + scope=user:email + state]
    Redir --> User[用户在 GitHub 授权]
    User --> CB[GET /auth/github/callback?code=...&state=...]

    CB --> Verify{state cookie<br/>== query state?}
    Verify -- 否 --> Err1[/400 invalid oauth state/]
    Verify -- 是 --> Clear[清掉 oauth_state cookie]

    Clear --> Exchange[GitHub.ExchangeCode → access_token]
    Exchange --> GetUser[GitHub.GetUser → ID/Login/AvatarURL]

    GetUser --> Lookup[OnboardSvc.GetUserTenants github_id]

    Lookup --> Branch{用户存在 + 有 tenant?}
    Branch -- 是 老用户 --> ReturnFlow
    Branch -- 否 --> AutoJoin[OnboardSvc.AutoJoinDefaultTenant<br/>找/建 default tenant 并自动入驻]
    AutoJoin -- 成功 --> NewFlow
    AutoJoin -- 失败 --> Onboard

    subgraph ReturnFlow["返流: 已注册用户"]
        R1[选 non-default tenant 优先]
        R2[issueTokenPair: refresh + access]
        R3[setRefreshCookie: HttpOnly · Secure · SameSite]
        R4[302 → frontend/auth/callback?access_token=JWT]
    end

    subgraph NewFlow["新用户已自动入驻 default tenant"]
        N1[role=member<br/>若 GlobalAdmin 名单匹配则 owner]
        N2[issueTokenPair]
        N3[setRefreshCookie]
        N4[302 → frontend/auth/callback?access_token=JWT]
    end

    subgraph Onboard["前端引导建租户"]
        O1[SignOnboarding: 短 TTL 仅含 github 信息]
        O2[302 → frontend/auth/callback?onboarding_token=...&github_login=...]
        O3[前端弹建租户表单]
        O4[POST /auth/onboard/create-tenant<br/>onboarding_token + tenant_name]
        O5[后端 VerifyOnboarding · 创建 tenant + 入驻 + 签 access JWT]
    end
```

**三种产物对比**

| 类型 | TTL | 载荷 | 用途 |
|---|---|---|---|
| access JWT (RS256) | 短（分钟级） | `sub, tid, role, global_role, ava, ghl, jti` | 业务请求 `Authorization: Bearer` |
| refresh token | 长（HttpOnly cookie） | server 端 token_store 里的随机串 | `/auth/refresh` 续 access |
| onboarding JWT (RS256) | 极短 | `github_id, github_login, avatar_url` | 仅用于建租户那一次 |

### 12.2 已认证请求的中间件链

```mermaid
sequenceDiagram
    autonumber
    participant Client
    participant Gin as Gin Router
    participant CORS as CORS
    participant Trace as TraceMiddleware
    participant JWT as JWTMiddleware
    participant Inj as InjectTenantContext
    participant Active as RequireActiveTenant
    participant Role as RequireTenantRole/Global
    participant H as Handler

    Client->>Gin: HTTP req + Authorization: Bearer <jwt>
    Gin->>CORS: CORS 校验
    CORS->>Trace: 注入 trace_id (header / 新生成)<br/>写访问日志 latency
    Trace->>JWT: 进入认证

    JWT->>JWT: parts := split "Bearer "
    JWT->>JWT: jwtService.Verify(token) 用 RSA public key
    alt 缺/无效/过期
        JWT-->>Client: 401 missing or invalid token
    else 有效
        JWT->>JWT: c.Set auth.sub / auth.tenant_id / auth.role / auth.global_role / auth.jti
    end

    JWT->>Inj: 进入 InjectTenantContext
    Inj->>Inj: 读 c.Get auth.* → 构 tenantdb.TenantContext
    Inj->>Inj: ctx = WithTenant + reqctx.WithTenantID
    Note right of Inj: 关键! tenantdb 后续在 DB 查询时<br/>会 SET LOCAL search_path

    Inj->>Active: 路由若挂了 RequireActiveTenant
    Active->>Active: SELECT status FROM public.tenants WHERE id=$1
    alt status != active
        Active-->>Client: 403 租户已被禁用
    else active
        Active->>Role: 进入角色守卫
    end

    Role->>Role: rank[role] >= rank[minRole]?
    alt 不足
        Role-->>Client: 403 insufficient role
    else 足
        Role->>H: 进入业务 handler
        H->>H: 处理请求 (DB 查询自动 SET search_path)
        H-->>Client: 200 / 4xx / 5xx
    end
```

### 12.3 授权三层模型

```mermaid
flowchart TB
    Req[请求 + JWT claims] --> L1{global_role}
    L1 -- global_admin --> Pass1[跨租户全权]
    L1 -- 空 --> L2

    L2{tenant_role 等级<br/>owner=3 > admin=2 > member=1} -- 不足 minRole --> R403_role[/403 insufficient tenant role/]
    L2 -- 足 --> L3

    L3{租户激活态}
    L3 -- status != active --> R403_tenant[/403 租户已被禁用/]
    L3 -- active --> L4

    L4{资源所有者校验}
    L4 -- 业务层 user_id != claims.sub --> R403_owner[/403 not owner/]
    L4 -- ok --> Allow[执行业务]
```

**职责分配**

| 检查项 | 责任层 | 实现 |
|---|---|---|
| 是否登录 | middleware | `JWTMiddleware` 验签 + Bearer |
| 全局管理员 | middleware | `RequireGlobalAdmin` 检查 `auth.global_role` |
| 租户角色门槛 | middleware | `RequireTenantRole(minRole)` 比较 rank |
| 租户激活态 | middleware | `RequireActiveTenant` 查 `public.tenants.status` |
| 资源归属 | application 层 | service 比较 `userID == record.UserID`（如 conversation 仅 owner 可读写） |
| 多租户隔离 | infrastructure | `tenantdb.SET LOCAL search_path = tenant_<id>` —— 跨租户访问物理上读不到表 |

### 12.4 RBAC 角色矩阵

```mermaid
classDiagram
    class GlobalRole {
        global_admin
        (空)
    }
    class TenantRole {
        owner
        admin
        member
    }
    class Resource {
        Tenants
        Users / Onboarding
        Agents / Conversations
        Knowledge / Skills / MCP
        Memories
    }

    GlobalRole --> Resource: 跨租户读写所有资源
    TenantRole --> Resource: 在自己 tenant_<id> schema 内读写
    Resource: owner-only: 删租户/转让/计费
    Resource: admin: 管理用户与配置
    Resource: member: 只能用业务功能
```

### 12.5 Token 续期与失效

```mermaid
sequenceDiagram
    autonumber
    participant FE as 前端
    participant API as /auth/refresh
    participant TStore as token_store (DB)
    participant JWT as JWTService

    Note over FE: access JWT 401 expired
    FE->>API: POST /auth/refresh<br/>(自动带 HttpOnly refresh cookie)
    API->>API: 取 cookie raw_rt
    API->>TStore: lookup hash(raw_rt)<br/>未撤销 + 未过期 + 用户存在
    alt 校验失败
        API-->>FE: 401 → 跳登录
    else ok
        API->>JWT: Sign 新 access JWT (RS256)
        API->>TStore: 旋转 refresh token<br/>写新行 + 撤销旧行 (一次性)
        API->>FE: SetCookie 新 refresh + body 新 access
    end

    Note over FE: 用户登出
    FE->>API: POST /auth/logout
    API->>TStore: 撤销当前 refresh token<br/>(可选: 把 jti 加入 access blacklist)
    API->>FE: 清 cookie + 200
```

**安全要点**

- access JWT 用 **RS256**：边缘网关只需公钥即可验签，不接触私钥
- refresh token 走 **HttpOnly + Secure + SameSite cookie**，不暴露给 JS
- token_store 存 hash 不存原值；轮换时旧值立即作废（refresh token rotation）
- onboarding token 单次使用：建租户成功后即作废
- 前端**禁止把 access JWT 写 localStorage**（CLAUDE.md 已有约束）；存内存 Context

### 12.6 关键代码索引

| 关注点 | 路径 |
|---|---|
| GitHub OAuth 重定向/回调 | `api/http/handler/auth_oauth_handler.go` |
| 登录注册/会话/租户 handler | `api/http/handler/auth_session_handler.go` · `auth_register_handler.go` · `auth_tenant_handler.go` |
| JWT 签发与验签 | `internal/iam/application/jwt_service.go` |
| OnboardService 自动入驻 | `internal/iam/application/onboard_service.go` |
| GitHub Client | `internal/iam/infrastructure/oauth/github.go` |
| Token 持久化 | `internal/iam/infrastructure/persistence/token_store.go` |
| JWT Middleware | `api/middleware/jwt.go` |
| Tenant 注入 | `api/middleware/inject_tenant.go` |
| 角色守卫 | `api/middleware/require_role.go` |
| 租户激活检查 | `api/middleware/require_active_tenant.go` |
| Outbox poller | `internal/memory/infrastructure/pipeline/outbox_poller.go` |
| Embedder worker | `internal/memory/infrastructure/pipeline/embedder.go` |
| Enricher worker | `internal/memory/infrastructure/pipeline/enricher.go` |
| 管道编排 | `internal/memory/infrastructure/pipeline/pipeline.go` |
| 事件结构 | `internal/memory/infrastructure/pipeline/events.go` |
| JetStream 配置 | `internal/memory/infrastructure/pipeline/jetstream.go` |

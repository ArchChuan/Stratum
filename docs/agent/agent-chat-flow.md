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

## 11. 用户记忆能力（业务流程 + 实现细节）

Stratum 的"用户记忆"分为**短期**（按 `conversation_id` 直读 `chat_messages`）与**长期**（异步管道沉淀到 `memory_entries` / `memory_entities` / `memory_summaries` / Milvus 向量库）两层。  
代码主路径在 `internal/memory/{application,infrastructure}` 和 `internal/agent/application/agent.go`，由 `api/wiring/memory.go` 按 Container 装配；写、读、召回、删除四条业务流彼此解耦但共用同一份数据。

### 11.1 数据归属与存储一览

| 存储 | 表 / Collection | 写入方 | 读取方 | 备注 |
|---|---|---|---|---|
| PG · tenant schema | `chat_messages` | `ChatStore.AddMessage`（同事务） | `BaseAgent.Execute` 加载历史；前端 `/conversations/:id/messages` | 短期记忆唯一源 |
| PG · tenant schema | `memory_outbox` | `ChatStore.AddMessage`（同事务） | `OutboxPoller` 出队 | 解耦"消息可见 ⟺ 事件可发" |
| PG · tenant schema | `memory_entries` | `EnricherWorker.persistEnrichment` | `MemoryInjector.BuildContext`、`RecallHandler.textSearch` | 长期记忆富化结果（importance / keywords / token_estimate） |
| PG · tenant schema | `memory_entities` | `EnricherWorker.persistEnrichment`（按 entity 循环 UPSERT） | `MemoryInjector.BuildContext` | 命名实体表，scope = user / agent |
| PG · tenant schema | `memory_summaries` | `EnricherWorker.writeSummary`（条件触发） | `MemoryInjector.BuildContext` | 会话累计 token 超过阈值时滚动生成 |
| PG · tenant schema | `memory_extraction_queue` | `MessageBuffer.flush` | （TODO，`BuildMemoryWorkers` 暂未上线 worker） | LLM 事实抽取队列 |
| PG · tenant schema | `memory_facts` | `MemoryService.ExtractFacts` | `MemoryService.ForgetMemory` / `Clear*` | 事实抽取产物，soft-delete |
| Milvus | `memory_<tenantID>`（短横线换下划线） | `EmbedderWorker` 通过 `MilvusVectorAdapter.Upsert` | `RecallHandler.tryVectorSearch` | 消息维度向量；scope/agent_id 走 metadata 过滤 |
| Milvus | `memory_facts_<tenantID>` | `MemoryService.ExtractFacts` | `ForgetMemory` / `ClearUserMemories` 清理 | 事实维度向量 |

> 命名规则：tenantID 含短横线时 Milvus collection 用下划线替换（见 `pkg/milvus/collections.go`、`api/wiring/memory.go: DeleteAllByUser`）。

### 11.2 整体业务全景

```mermaid
flowchart LR
    subgraph Write["写入路径"]
        direction TB
        H[BaseAgent.Execute]
        H --> CS[ChatStore.AddMessage<br/>同事务双写 chat_messages + memory_outbox]
        CS -.异步.-> OP[OutboxPoller<br/>NATS publish memory.raw.<tenantID>]
        OP --> EM[EmbedderWorker<br/>Embed + Milvus.Upsert<br/>publish memory.enriched.<tenantID>]
        EM --> EN[EnricherWorker<br/>LLM 富化 → memory_entries/entities<br/>条件触发 memory_summaries]
    end

    subgraph Read["读取路径"]
        direction TB
        I[BaseAgent.Execute 启动时]
        I --> Inj[MemoryInjector.BuildContext<br/>读 summaries + top entities]
        Inj --> Sys[拼入 SystemPrompt 前缀<br/>--- Memory Context ---]
    end

    subgraph Recall["工具召回（ReAct 内）"]
        direction TB
        TC[stratum_recall_memory 工具]
        TC --> RF[BaseAgent.RecallMemoryFn]
        RF --> RH[RecallHandler.Handle<br/>1 vector search<br/>2 fallback ILIKE]
    end

    subgraph Clear["清理路径"]
        direction TB
        API[POST /memory/clear · DELETE 资源]
        API --> MS[MemoryService.ClearUserMemories<br/>/ ClearAgentMemories / ForgetMemory]
    end
```

四条路径在请求/事件层面解耦：写入与读取通过 PG + Milvus 持久化层联系；同步链路（agent 主流程）只触发"短期写"与"系统提示词注入"，长期富化全部走 NATS JetStream 异步。

### 11.3 写入主路径：chat_messages 与 memory_outbox 同事务双写

入口：`PgChatStore.AddMessage`（`internal/agent/infrastructure/persistence/chat_store.go`），由 `BaseAgent.Execute` 在 ReAct 收尾时调用两次（user、assistant 各一条）。

```mermaid
sequenceDiagram
    autonumber
    participant Base as BaseAgent.Execute
    participant CS as PgChatStore.AddMessage
    participant TX as PG TX (tenant schema)
    participant OB as memory_outbox

    Base->>CS: AddMessage(user)
    CS->>TX: BEGIN · SET LOCAL search_path = tenant_<id>, public
    TX->>TX: UPDATE chat_conversations<br/>set updated_at, expires_at = NOW()+30d
    TX->>TX: INSERT chat_messages RETURNING id, created_at
    alt 过滤通过 memoryOutboxContent(role, content)
        TX->>OB: INSERT memory_outbox(message_id, payload=JSON)
        Note over OB: payload 字段：<br/>message_id, conversation_id, tenant_id,<br/>role, content, created_at, user_id,<br/>agent_id, scope
    else 跳过原因
        Note over CS: role 不在 user/assistant<br/>或 runes < MemoryOutboxMinRunes<br/>或 IsError / SkipOutbox
        TX-->>TX: 只写 chat_messages
    end
    TX->>TX: COMMIT
    CS-->>Base: msg.ID, msg.CreatedAt
    Base->>CS: AddMessage(assistant) （同上）
```

**关键设计要点**

| 设计点 | 含义 / 代码位置 |
|---|---|
| 同事务双写 | `chat_messages` 与 `memory_outbox` 在同一 `BEGIN…COMMIT` 内插入。chat_store.go 内的 `outboxQueued / outboxSkipReason` 双变量给后续日志使用。 |
| Outbox 过滤 | `memoryOutboxContent` 只允许 `user` / `assistant`、`runes >= MemoryOutboxMinRunes`，并按 `MemoryOutboxMaxRunes` 截断；`SkipOutbox=true` 或 `IsError=true` 直接跳过。 |
| Tenant 路由 | `execTenantID` 强制 `SET LOCAL search_path = "tenant_<id>", public`，并对 tenant_id 做字符白名单校验。 |
| 用户/Agent 元信息 | `ChatMessage.UserID` / `AgentID` / `MemoryScope` 在 `BaseAgent.Execute` 写消息时一并传入，payload 直接随 outbox 落库，下游 worker 不再二次查询。 |
| 失败回滚 | `tx.Rollback` 由 `execTenantID` 兜底，写失败时 `chat_messages` 与 `memory_outbox` 一起回滚——避免"消息可见但事件丢失"或"事件已发但消息丢失"。 |

### 11.4 异步管道：Outbox → Embedder → Enricher

管道生命周期由 `cmd/server` Harness 控制；构造在 `api/wiring/memory.go: buildMemory`，在 `MEMORY_PIPELINE_ENABLED=true` 且 NATS / Milvus 可达时启动。

```mermaid
flowchart LR
    OB[(memory_outbox<br/>tenant_*)] -- ListTenantSchemas + FOR UPDATE SKIP LOCKED --> Poll[OutboxPoller<br/>Tick = MemoryOutboxPollInterval]
    Poll -- Publish memory.raw.<tenantID><br/>+ DELETE id ANY ids --> RawSubj((memory.raw.&lt;tid&gt;))
    RawSubj --> RawStream[(JetStream MEMORY_RAW)]
    RawStream -- Fetch 1 / 5s --> Emb[EmbedderWorker × N<br/>EmbedderConsumerName]
    Emb -- Upsert(tenantID,userID,msgID,vec,metadata) --> Milvus[(Milvus memory_&lt;tid&gt;)]
    Emb -- Publish memory.enriched.<tenantID> --> EnrSubj((memory.enriched.&lt;tid&gt;))
    EnrSubj --> EnrStream[(JetStream MEMORY_ENRICHED)]
    EnrStream -- Fetch 1 / 5s --> Enr[EnricherWorker × N<br/>EnricherConsumerName]
    Enr -- LLM.Complete(T=0.1) --> LLM[(per-tenant Gateway)]
    Enr -- UPSERT --> Entries[(memory_entries)]
    Enr -- UPSERT 循环 --> Entities[(memory_entities)]
    Enr -. token 超阈值 .-> Summary[(memory_summaries)]
```

#### 11.4.1 OutboxPoller — 全租户轮询、SKIP LOCKED、至少一次

`internal/memory/infrastructure/pipeline/outbox_poller.go`

- 每 `MemoryOutboxPollInterval` 触发一次：`tenantdb.ListTenantSchemas` 拿全部 `tenant_xxx`，逐个调用 `pollTenant`。
- 每个 tenant 单独事务：`SET LOCAL search_path`，`SELECT id, payload FROM memory_outbox ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`。
- 每条记录：`json.Unmarshal` → `MemoryRawEvent`，组装 subject `memory.raw.<tenantID>`，调用 `js.Publish`（带 `MemoryOutboxPublishTimeout` 超时）；publish 失败立刻 `return err`（事务回滚，下一轮重发，至少一次）。
- 全部 publish 成功后 `DELETE FROM memory_outbox WHERE id = ANY($1)`，最后 `COMMIT`；Prometheus 计数 `outboxPublished{tenant_id, status}`。
- 多实例并发：靠 `FOR UPDATE SKIP LOCKED`，互不阻塞——"谁锁到谁负责"。

#### 11.4.2 EmbedderWorker — 向量化 + Milvus Upsert + 转发

`internal/memory/infrastructure/pipeline/embedder.go`

```mermaid
flowchart TB
    Fetch([Fetch 1 msg from MEMORY_RAW]) --> Unmar[UnmarshalRawEvent]
    Unmar -- 解析失败 --> AckErr[msg.Ack 丢弃 + 计数 error]
    Unmar -- ok --> Resolve{embedSvc nil?}
    Resolve -- 否 --> UseGlobal[复用 embedSvc]
    Resolve -- 是 --> Per[embedResolver ctx, tenantID]
    Per --> Skip{nil?}
    Skip -- 是 --> AckSkip[msg.Ack 跳过 + log warn<br/>该 tenant 未配 embedding]
    Skip -- 否 --> UseGlobal
    UseGlobal --> Em[embedSvc.EmbedVector content]
    Em -- err --> Nak1[msg.Nak · backoff Base→Max]
    Em -- ok --> CheckV{vectorDB nil?}
    CheckV -- 是 --> AckSkip2[Ack 跳过]
    CheckV -- 否 --> Up[vectorDB.Upsert<br/>tenantID/userID/msgID/vec + metadata]
    Up -- err --> Nak1
    Up -- ok --> Marsh[Marshal MemoryEnrichedEvent VectorID=MessageID]
    Marsh --> PubE[js.Publish memory.enriched.<tenantID>]
    PubE -- err --> Nak1
    PubE -- ok --> Ack[msg.Ack · 计数 success + 记录 latency]
```

- Embedding client 优先用 wiring 注入的全局 `embedSvc`（当前 wiring 传 nil），否则用 `embedResolver(ctx, tenantID)` 解析的租户专属客户端；二者都拿不到时直接 Ack 跳过，避免无效重投。
- Milvus collection 名按 tenant 取，写入 metadata：`conversation_id / user_id / agent_id / scope / role / content / created_at(RFC3339)`；向量 ID = `MessageID`，与 `chat_messages.id`、未来的 `memory_entries.id` 同源。
- 失败统一 `msg.Nak`，由 JetStream 重投递；达到 `MaxDeliver` 后进 DLQ。`safeProcessMessage` 用 `defer recover` 把单条 panic 隔离在一条消息内。
- Fetch 拉空 / 出错时按 `MemoryFetchBackoffBase → Max` 退避；`sleepCtx` 监听 `ctx.Done` 与 `stopCh`，保证关闭信号能立即生效。

#### 11.4.3 EnricherWorker — LLM 富化 + 实体写入 + 摘要回滚

`internal/memory/infrastructure/pipeline/enricher.go`

```mermaid
sequenceDiagram
    autonumber
    participant W as EnricherWorker
    participant JS as JetStream MEMORY_ENRICHED
    participant LLM as per-tenant Gateway
    participant DB as PG tenant schema

    W->>JS: Fetch 1 msg
    W->>W: UnmarshalEnrichedEvent
    W->>LLM: Complete(model=EnrichModel,<br/>Temperature=0.1, timeout=MemoryEnrichLLMTimeout)
    Note right of LLM: 返回 JSON：<br/>{importance, keywords[], entities[], token_estimate}
    LLM-->>W: content (JSON)
    alt JSON 解析或 LLM 失败
        W->>JS: msg.Nak
    else 成功
        W->>DB: BEGIN · SET LOCAL search_path
        W->>DB: INSERT memory_entries ON CONFLICT(id) DO UPDATE<br/>type=long_term, importance, keywords, token_estimate, enriched_at, conversation_id, created_at
        loop 每个 entity
            W->>DB: INSERT memory_entities ON CONFLICT(name, entity_type, user_id, COALESCE(agent_id,''))<br/>DO UPDATE last_seen_at = NOW()
        end
        W->>DB: COMMIT
        W->>JS: msg.Ack

        Note over W,DB: 摘要触发：必须在 Ack 之后 + 独立事务执行
        W->>W: runSummaryAsyncSafe (defer recover)
        W->>DB: 短 tx 读取 SUM(token_estimate) + 历史消息
        alt 累计 ≥ SummaryTokenThreshold<br/>且近 1 分钟无新摘要
            W->>LLM: Complete(SummaryModel, T=0.3, timeout=MemorySummaryLLMTimeout)
            LLM-->>W: summary 文本
            W->>DB: 独立 tx INSERT memory_summaries<br/>conversation_id, user_id, agent_id, scope, summary, covered_until, token_count
        else 阈值未达 / 已有近期摘要
            W-->>W: 直接返回 nil
        end
    end
```

**摘要旁路的关键修复**（来自 `enricher.go: maybeTriggerSummary` 注释）

> 旧实现把 LLM Complete 塞进 `persistEnrichment` 的事务里，单条记录持锁 30s+，高 QPS 下 pgxpool 连接耗尽。现在拆成三段：短 tx 读阈值与历史 → 解锁后调 LLM → 另起 tx 写摘要；并且必须发生在 `msg.Ack` 之后，失败只 warn，绝不影响主富化结果。

**重要不变量**

- `memory_entries.id = chat_messages.id = MemoryEnrichedEvent.MessageID = Milvus vector_id`，三库通过同一 UUID 串联。
- `memory_entities` 唯一键 `(name, entity_type, user_id, COALESCE(agent_id, ''))`：scope=user 时 `agent_id` 为空，scope=agent 时按 agent 隔离。
- LLM 不可控副作用全部隔离在富化阶段；ChatStore 与 Embedder 不会调用 LLM，主链路绝不被 LLM 延迟阻塞。
- `safeProcessMessage` 与 `runSummaryAsyncSafe` 两道 `defer recover` 把"单条数据导致 worker 崩溃"的风险吃掉。

### 11.5 备用写入通道：MessageBuffer + 事实抽取（演进中）

`internal/memory/application/message_buffer.go` + `extraction.go`：与上面的"消息维度"管道并行，用于**事实级抽取**（更精细，但当前 worker 未在 `BuildMemoryWorkers` 中启用，处于演进状态）。

```mermaid
flowchart LR
    Call[MemoryService.BufferMessage] --> RPush[Redis RPUSH<br/>key memory:buffer:<tid>:<uid>:<aid>:<conv>]
    RPush --> Cond{LLEN ≥ MemoryBufferFlushSize<br/>OR 最旧消息 age ≥ MemoryBufferFlushInterval?}
    Cond -- 否 --> Wait[直接返回]
    Cond -- 是 --> Flush[flush: LRANGE 0 -1 + Enqueue ExtractionTask + DEL key]
    Flush --> Q[(PG memory_extraction_queue)]
    Q -. TODO: ExtractionWorker .-> EF[MemoryService.ExtractFacts]
    EF --> LLMx[LLMExtractor.ExtractFacts]
    LLMx --> Norm[normalizeEntity<br/>trigram 模糊匹配 + IncrementFactCount]
    Norm --> InsertF[FactRepo.Create memory_facts]
    InsertF --> EmbedF[EmbedClient.Embed]
    EmbedF --> MFV[(Milvus memory_facts_<tid>)]
```

- **缓冲键**：`memory:buffer:<tenant>:<user>:<agent>:<conv>`，按会话维度合批，避免单条消息频繁触发 LLM。
- **触发条件**：批数 ≥ `MemoryBufferFlushSize` 或最早消息超过 `MemoryBufferFlushInterval` 即 flush。flush 完成后立刻 `DEL` 整个 key。
- **`ExtractFacts`**：拼接所有消息 → LLM 抽取事实数组 → 对每个事实：`FindSupersedeCandidates`（trigram 相似度，supersede 决策 TODO） → `normalizeEntity`（fuzzy match 升级旧实体 fact_count，否则新建） → `domain.NewFact` 校验后写 `memory_facts` → `EmbedClient.Embed` 后写 `memory_facts_<tenantID>`。
- **现状**：`BuildMemoryWorkers` 仍是空切片（占位 TODO），表示 ExtractionWorker / SupersedeWorker / ProfileWorker / GCWorker 暂未在 Container 中拉起；这条通道目前由 API 直接调用 `BufferMessage` / `ExtractFacts` 触发，agent 主链路不依赖它。

### 11.6 读取路径 A：系统提示词注入

`BaseAgent.Execute` 启动时通过 `MemoryInjector.BuildContext`（`internal/memory/infrastructure/pipeline/injector.go`）把"近期摘要 + 高频实体"注入 system prompt。

```mermaid
sequenceDiagram
    autonumber
    participant Base as BaseAgent.Execute
    participant Inj as MemoryInjector.BuildContext
    participant DB as PG tenant schema

    Base->>Inj: ic InjectionContext<br/>(TenantID, UserID, AgentID, ConversationID, Query, Scope)
    Inj->>DB: BEGIN · SET LOCAL search_path = tenant_<id>, public
    Inj->>DB: SELECT summary FROM memory_summaries<br/>WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1
    alt scope == "agent" && agentID 非空
        Inj->>DB: SELECT name FROM memory_entities<br/>WHERE user_id=$1 AND agent_id=$2<br/>AND scope='agent' AND status='active'<br/>ORDER BY last_seen_at DESC LIMIT EnricherTopEntities
    else
        Inj->>DB: SELECT name FROM memory_entities<br/>WHERE user_id=$1 AND scope='user' AND status='active'<br/>ORDER BY last_seen_at DESC LIMIT EnricherTopEntities
    end
    Inj-->>Base: "[Memory Context]\nSummary: ...\nKey Entities: a, b, c\n"
    Base->>Base: BuildContextMessages(systemPrompt, memCtx, history, input, ...)
```

- 全部走只读事务，失败只 `Warn` 不阻塞主链路（`agent.go: memory injection failed`）。
- 摘要与实体都空时返回空串，避免在 prompt 上加噪声。
- `Scope` 由 agent 配置中的 `MemoryScope` 决定（user 全局共享 / agent 按代理隔离），同一 UserID 不同 Agent 在 scope=user 下可共享实体，scope=agent 下完全独立。

### 11.7 读取路径 B：stratum_recall_memory 工具召回

ReAct 期间，LLM 可以选择调用 `stratum_recall_memory` 工具——agent.go 在 `MemoryInjector != nil` 时把它注册到 `availableTools`，handler 是 `RecallHandler.Handle`（`internal/memory/infrastructure/pipeline/recall_tool.go`），由 `api/wiring/memory.go` 暴露为 `RecallMemoryFn`。

```mermaid
flowchart LR
    Tool[LLM 调用 stratum_recall_memory<br/>input: query + limit] --> Fn[BaseAgent.RecallMemoryFn<br/>注入 tenantID/userID/agentID/scope]
    Fn --> H[RecallHandler.Handle]
    H --> Try{embedSvc + vectorDB 可用?}
    Try -- 否 --> Text[textSearch]
    Try -- 是 --> Em[EmbedVector query]
    Em -- err --> Text
    Em -- ok --> Filter{构造 Milvus expr}
    Filter --> Vec[vectorDB.SearchWithFilter<br/>collection=memory_<tid>,<br/>topK=MemoryLongTermTopK]
    Vec -- 命中 --> JsonV[json.Marshal RecallEntry array]
    Vec -- 空/err --> Text
    Text --> SQL[BEGIN · SET search_path<br/>SELECT content, role, importance, created_at<br/>FROM memory_entries<br/>WHERE enriched_at IS NOT NULL<br/>AND content ILIKE %query%<br/>AND user_id = $<br/>AND (scope='user' OR scope='agent' + agent_id)]
    SQL --> Order[ORDER BY importance DESC, created_at DESC LIMIT req.Limit]
    Order --> JsonT[json.Marshal 或 "No relevant memories found."]
    JsonV --> Out[作为 role=tool 消息回填 ReActState.Messages]
    JsonT --> Out
```

- **向量优先 + 降级 ILIKE**：embedding / Milvus 可用时走语义召回；任何环节失败（embedding error / vector 空命中）平滑降级到全文 ILIKE。
- **scope 过滤**：scope=agent 时叠加 `agent_id == "<id>"`；user_id 含引号或反斜杠时直接拒绝（防注入）。
- **限流**：`limit` 默认 5，最大 20；vector 路径用 `constants.MemoryLongTermTopK`。
- **可观测**：`memory.recall.vector` / `memory.recall` 两段 Debug 日志，带 trace_id、tenant_id、query、命中数。
- 工具结果作为 `role=tool` 消息回填 `ReActState.Messages`，下一轮 LLM 决定继续调用工具还是直接产出答案。

### 11.8 删除与清理路径

| API / 触发 | 调用 | 影响范围 |
|---|---|---|
| `MemoryService.ForgetMemory(factID)` | `FactRepo.Update(MarkDeleted)` + Milvus `memory_facts_<tid>` 删 ID（best-effort） | 单条事实 soft-delete；孤立向量由 GC worker 兜底 |
| `MemoryService.ClearUserMemories` | `FactRepo.DeleteAllByUser` + Milvus `memory_<tid>` `DeleteByFilter("user_id == ..." )` + `MemoryRepo.DeleteAllByUser` + `EntityRepo.DeleteAllByUser` | 同租户内某 user 全量硬删（facts + entries + entities + 向量） |
| `MemoryService.ClearAgentMemories` | `FactRepo.DeleteAllByAgent` 返回 IDs + Milvus `memory_facts_<tid>.Delete(IDs)` + `MemoryRepo.DeleteAllByAgent` | 单 agent 全量硬删；当前 entities 不按 agent 删（按 CLAUDE.md 历史记录是未完成项） |
| DELETE conversation | `PgChatStore.DeleteConversation` | 删 `chat_messages` + `chat_conversations`；不级联清 long-term（依赖上面三个 API 主动调用） |

> 多租户路由：所有删除调用都通过 `execTenant(ctx, tenantID, fn)` 或 `execTenantID` 切到 `tenant_<id>` schema；Milvus collection 名按 tenantID 拼接（短横线 → 下划线）；缺一个就会把删除请求打到 public schema，复现历史 SQLSTATE 42P01 bug。

### 11.9 失败语义与背压（端到端）

| 位置 | 失败语义 | 防护 / 兜底 |
|---|---|---|
| `ChatStore.AddMessage` 同事务双写 | 业务 tx 回滚 → 既不留消息也不留 outbox | 强一致；调用方只看到一个 error |
| `OutboxPoller.pollTenant` publish 失败 | tx 回滚保留 outbox 行 | 下一轮重发；多实例并发用 `FOR UPDATE SKIP LOCKED` |
| `OutboxPoller` json.Unmarshal 失败 | 仅日志 + 把 id 加入待删列表（脏数据自动出队） | 不会卡死队列；用 metrics 监控 |
| `EmbedderWorker` Embedding 失败 | `msg.Nak` + backoff 重投 | 达到 `MaxDeliver` 进 DLQ |
| `EmbedderWorker` Milvus 失败 | 同上 | vectorDB 故障时只影响向量化，PG 主链路不受影响 |
| `EmbedderWorker` publish enriched 失败 | 同上 | "已 Upsert Milvus 但未发 enriched"会等下次重投；Upsert 幂等可重放 |
| `EnricherWorker` LLM 失败 / JSON 解析失败 | `msg.Nak` 重试，超过 `MaxDeliver` 进 DLQ | 不污染数据库 |
| `EnricherWorker` persistEnrichment 失败 | tx 回滚 + `msg.Nak` | 至少一次写；`ON CONFLICT DO UPDATE` 保证幂等 |
| `EnricherWorker` 摘要触发失败 | `runSummaryAsyncSafe` 只 warn | 主富化已 Ack，绝不返工；摘要下次累计达阈值再尝试 |
| `MemoryInjector.BuildContext` 失败 | agent.go 捕获并 `Warn`，memCtx 留空 | 主链路不阻塞 |
| `RecallHandler.tryVectorSearch` 失败 | 自动降级到 `textSearch` | 始终返回结果（可能为空字符串） |
| `Pipeline.Stop()` | 关闭独立 ctx + WaitGroup | outbox 残留行下次重启续传；JetStream 持久化保留未 Ack 消息 |

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

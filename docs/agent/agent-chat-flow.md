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

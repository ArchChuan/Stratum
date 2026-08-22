# 上下文压缩复用设计（Context Compaction Reuse）

> 日期：2026-08-21
> 范围：组装侧 `BuildContextMessagesWithCompaction` 与循环侧 `compactLoopMessagesWithPolicy` 的压缩链路改造，引入跨轮复用的压缩摘要存储，修正窗口计数、工具配对、工具数据渲染与防污染四类缺陷。
> 状态：方案（待实现，非代码交付）

---

## 1. 背景与问题

### 1.1 症状

1. **每轮重压最老段**：会话历史超过 `DefaultContextHistoryWindow = 50` 条后，组装侧每次请求都对**最老的溢出段重复压缩**，即使该段从未变化。压缩产出的摘要不落库、不跨轮复用。
2. **工具数据在压缩层信息丢失**：压缩 LLM 唯一能看到的是 `renderConversation` 渲染的纯文本。循环侧 ToolCalls 消息 `Content` 为空 → 渲染成 `"Assistant: \n"`，**工具名与参数全部丢失**；组装侧转换 `ChatMessage → LLMMessage` 只取 `Role`/`Content`，丢 `StepsJSON`。
3. **组装侧无工具配对保护**：组装侧按消息逐条截断，可能把 assistant 工具调用与其 tool 结果从中间切断，破坏配对。
4. **工具摘要堆成污染源**：`buildToolObservationSummary` 产出的内部工具摘要消息（每轮 3000 rune 截断）随 `ListMessages` 全量加载，工具数据在历史里**层层叠加**。

### 1.2 根因链

| # | 根因 | 证据 |
|---|------|------|
| R1 | **窗口计数用 DB 全量**：`BuildContextMessagesWithCompaction` 以 `len(history) > historyWindow` 判断溢出，而 `history` 来自 `ListMessages`（无 LIMIT、不过滤 visibility、含 internal 工具摘要消息），不是「未压缩轮数」 | context_budget.go:140-143；chat_store.go:329-364 |
| R2 | **压缩产物不落库**：两侧压缩的摘要只拼进当次 prompt，无存储、无游标、无版本，同源消息每轮重复压缩 | compaction.go:281-308；context_budget.go:213-215 |
| R3 | **渲染丢弃工具信息**：`renderConversation` 只写 `m.Content`，ToolCalls 消息 Content 为空 → `"Assistant: \n"`；非 user/assistant/system 的 role（tool）原样输出 role 字符串 | history_compactor.go:183-200 |
| R4 | **组装侧转换丢 StepsJSON**：`ChatMessage → LLMMessage` 只带 `Role`/`Content` | context_budget.go:169、:175 |
| R5 | **StepsJSON 列恒为空**：Go 侧从未写入 `steps_json`（仅 domain 定义、AddMessage 默认 `[]`、persist、read），前端 schema 定义了结构但后端不填充；「从 StepsJSON 重建工具轮」不可行 | domain/agent.go:143；chat_store.go:222-223、:250、:348；tenant_schema.sql:712 |
| R6 | **组装侧无配对/无最近 N 对保护**：逐条截断，无 `groupMessages` 式原子组 | context_budget.go:185-188 |

### 1.3 目标

1. **压缩摘要跨轮复用**：同源消息只压缩一次，后续轮直接读缓存摘要；组装侧与循环侧共用同一压缩摘要存储机制。
2. **窗口按「游标后未压缩轮数」计数**：DB 全量不再是窗口依据。
3. **工具调用对配对原子**：工具调用与其结果作为整体进入压缩或保留，不被切断。
4. **保留关键信息**：工具因果链（tool name + 参数 + 结果摘要）与关键结果值（数字/状态/文件名等决策关键量）不因压缩丢失。
5. **防二次注入**：压缩 LLM 只消费已脱敏的 `Summary`/`ModelContent`，绝不读原始工具输出。
6. **业界对齐**：摘要持久化复用（LangGraph checkpointer / LangMem / Zep / Anthropic prompt caching）共识落地，工具数据读取对齐「配对渲染」范式。

---

## 2. 决策记录

| 编号 | 决策 | 理由 | 状态 |
|------|------|------|------|
| D1 | **复用机制只适用组装侧**：循环侧 `LLMMessage` 无 DB id，无法锚定游标 | 组装侧输入来自 DB（`[]*ChatMessage` 带 id），天然可锚定；循环侧输入为内存态，无 id 无法定位覆盖段 | 已确认 |
| D2 | **摘要放 system**：压缩摘要拼进 `systemFull`，不混入 history 尾部 | 摘要是对「此前全部对话」的连续压缩，属于指令级上下文；混入 history 尾部会与后续消息产生顺序歧义 | 已确认 |
| D3 | **窗口按轮计数**：游标（covered_until 消息 id）之后的未压缩轮数 > `historyWindow` 才溢出 | 修复 R1 缺陷；已压缩段被游标排除在窗口之外 | 已确认 |
| D4 | **保最近 N 对工具轮全保真**：`Summary + 最近 N 对全保真`；老段压成结构化摘要，最近 N 对工具轮原样保留 | 分层渐退（tiered degradation）；保证近期工具因果链完整可读 | 已确认 |
| D5 | **配对原子**：assistant(tool_calls) 与其 tool 结果作为整体单元，要么全压要么全留 | LangGraph/OpenAI API 硬约束：tool_call 后缺 tool result 直接 400 | 已确认 |
| D6 | **共享压缩摘要存储**：组装侧与循环侧共同复用同一压缩摘要存储机制（`CompactionSummaryStore`） | 业界共识：摘要作为一等资产落存储，消费方读缓存而非每次重压 | 已确认 |
| D7 | **独立建表，不复用 `memory_summaries`**：语义本质不同（对话连续性 vs facts/偏好）；schema 借鉴其锚定字段模式（covered_until/source_ids） | 数据源（chat_messages vs memory_entries）、消费方（prompt 组装 vs 记忆检索）、生命周期（单游标推进 vs tier 分级）均不同 | 已确认 |
| D8 | **防二次注入**：压缩输入只消费 `GuardedToolResult.Summary`（800 rune 脱敏）与 internal 工具摘要消息（基于 obs.Summary），不消费 `RawResult`/`StructuredContent`/原始上游响应体 | bearer credential 与 PII 不得进入下游错误正文/日志/上下文 | 已确认 |
| D9 | **渲染配对格式**：压缩渲染工具数据用 `[Tool] name(args) → result` 配对文本 | 业界对齐（见 §5.1）；修复 R3 丢参数 | 新增（本方案） |
| D10 | **StepsJSON 不重建工具轮**：组装侧工具数据保留依赖 internal 工具摘要消息原文；StepsJSON 如需成为结构化源头，作为独立后续任务补齐写入 | R5 事实：steps_json 恒空，重建不可行；本方案最小改动 | 新增（本方案） |

---

## 3. 分节设计

### 3.1 共享压缩摘要存储（CompactionSummaryStore）

**Schema**（tenant-only，`pkg/storage/postgres/tenant_schema.sql` 幂等应用，`IF NOT EXISTS`）：

```sql
CREATE TABLE IF NOT EXISTS chat_compaction_summaries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL,
    covered_until   uuid NOT NULL,            -- 覆盖到 chat_messages.id（游标，单调推进）
    summary         text NOT NULL,            -- 结构化摘要正文（见 §3.4 格式）
    source_start    uuid NOT NULL,            -- 覆盖段第一条消息 id
    source_end      uuid NOT NULL,            -- 覆盖段最后一条消息 id
    version         int  NOT NULL DEFAULT 1,  -- 段扩展时递增
    token_count     int  NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_chat_compaction_conversation ON chat_compaction_summaries (conversation_id);
```

设计要点：

- **每会话一条，covered_until 单调推进**：压缩是「最老段永不回滚」的线性过程，`UNIQUE(conversation_id)` + upsert 语义。覆盖段扩展时 `version++`。
- **借鉴 `memory_summaries` 锚定字段**（covered_until/source_start/source_end/source_ids 模式见 history_repo.go:40-44），但独立建表（D7）。
- **Repository 契约**（`internal/agent/domain/port`，tenantID 显式携带）：
  - `GetCoverage(ctx, tenantID, conversationID) (*CompactionCoverage, error)` — 读最新覆盖游标 + 摘要
  - `Upsert(ctx, tenantID, c *CompactionSegment) error` — 写/推进游标
  - 实现经 `execTenant(ctx, tenantID, fn)`，禁止绕过租户封装。

**读写时序（组装侧）**：

```
加载 history（ListMessages 全量）
  └─ GetCoverage(conversationID)
       ├─ 无覆盖 → 首次压缩，covered_until = 空
       ├─ 有覆盖 → covered_until 之前的段 → 直接用 summary 替换（不重压）
       │            covered_until 之后的段 → 未压缩轮 → 进窗口判断
       └─ 窗口 = 未压缩轮数 > historyWindow？
              ├─ 是 → 溢出段压缩 → 结构化摘要 → Upsert 推进 covered_until
              └─ 否 → 未压缩轮原文保留
```

**循环侧边界**（D1）：循环侧读组装侧已生成的摘要（初始消息来自组装侧输出）；循环内新增轮无 DB id，仍走内存压缩，**不写共享存储**。仅「从 DB 加载的锚定段」在循环侧溢出时可选回写（保留为后续项，本方案不实现）。

### 3.2 组装侧压缩改造（context_budget.go）

`BuildContextMessagesWithCompaction`（:122-227）改造：

1. **游标接入**：入口读 `CompactionCoverage`；`covered_until` 之前的消息段折叠为 1 条 system 摘要（D2），不进入窗口判断（D3）。
2. **splitRounds 轮划分**：把未覆盖段按轮划分——`user → assistant(+工具对)` 构成一轮；工具对由 `assistant(role=assistant, 非空 StepsJSON 或 internal 摘要关联)` 与其后 tool 消息组成原子组（D5）。组装侧输入含 internal 工具摘要消息（role=assistant, Visibility=Internal），轮划分需识别 internal 摘要并归入其所属轮。
3. **窗口溢出**：未压缩轮数 > `historyWindow` → 溢出最老轮（整轮为单位，不逐条）。
4. **最近 N 对保护**（D4）：溢出后保留最近 N 个工具轮原文（对齐循环侧 `compactLoopMessagesWithReserve` 的 `recentGroups`，compaction.go:95-105）；仅更老的轮进入压缩。
5. **压缩触发**：`compactor != nil && summaryReserve > 0 && len(droppedRounds) > 0`（保留原触发条件，:192-196，改为以轮计）。
6. **回写**：压缩后的结构化摘要 → `Upsert`，`covered_until` 推进到最后一条被压掉的 internal 工具摘要消息 id。
7. **降级路径**：预算耗尽仍走 `minimalHeadMessages`（:70-83）保 system+task；覆盖摘要读取失败必须 fail closed——降级为「无覆盖」重新走全量路径，禁止静默丢弃摘要。

### 3.3 循环侧对齐（compaction.go / history_compactor.go）

1. **渲染配对格式**（D9）：`renderConversation`（history_compactor.go:183-200）增加工具对渲染分支——`m.ToolCalls` 非空时输出 `[Tool] name(args) → result`，tool 消息输出 `result`；只读已脱敏内容（`guarded.ModelContent`，react_tool.go:702、:748），不读 raw。修复 `"Assistant: \n"` 丢参数缺陷（R3）。
2. **配对已对齐**：`groupMessages`（compaction.go:31-51）已把 assistant(ToolCalls) 吸收后续 tool 消息成原子组；`toEstimate`（:259-275）已折叠 ToolCalls name+arguments 进 token 估算——保持，并将 `recentGroups` 语义写入组装侧（对称）。
3. **共享预算账本**：两侧共享 `ComputeBudget` 四配额，保持现状；循环侧 `TokenCorrection` 与冷却保留。
4. **摘要 schema 对齐**：循环侧 `summarizeMiddle` 注入的 prompt 改用与组装侧一致的平台参数 `agent.compaction_prompt` + 工具配对渲染输出，保证两侧产出的摘要格式一致（共享存储的消费方不区分来源）。

### 3.4 结构化摘要 schema

摘要正文采用结构化文本（非自由散文），保证工具因果链与关键结果值可解析：

```
## 会话摘要 v{version}（覆盖至消息 #{covered_until}）

### 已达成决定
- ...

### 关键事实
- ...

### 未解决问题
- ...

### 工具因果链（最近保留段之外的摘要层）
- [步骤] 用户目标 → 工具 {name}({参数摘要}) → 结果 {Summary 截断}
```

- 各节对齐平台参数 `agent.compaction_prompt`（2026-08-22 起压缩提示词迁平台级，无代码常量兜底）「保留关键事实、已达成的决定、尚未解决的问题」。
- 工具行只含 `ToolName + Arguments 摘要 + Summary`（已脱敏），关键数值/状态保留，禁止带原始结果。
- 循环侧与组装侧共同遵循，是共享存储的写入契约（D6）。

### 3.5 安全约束

- **防二次注入**（D8）：压缩 LLM 的输入仅由 `renderConversation`（脱敏 Content）与 internal 工具摘要消息（obs.Summary）构成；`RawResult`/`StructuredContent` 永不进压缩。
- **日志**：压缩失败仅记 `layer.operation + 错误`，不记摘要正文、工具名或参数；bearer credential 不得进入下游错误正文。
- **fail closed**：覆盖读取失败 → 降级全量路径（宁可多压不可丢上下文）；压缩回写失败 → 本次返回降级（无摘要）但**必须记录并暴露**（risk-regression 原则 5），不得伪成功。
- **不可逆清理**：本方案无 DropCollection/删除操作；`chat_compaction_summaries` 随会话生命周期清理（对齐 `chat_messages` 清理路径，本方案不新增）。

---

## 4. 四链路断裂点 → v2 修正对照

| # | 断裂链路 | 现状 | 修正 |
|---|---------|------|------|
| 1 | 循环侧渲染丢参数 | react_tool.go:113-117（tool 结果进 s.Messages）→ compaction.go:31（groupMessages）→ history_compactor.go:183（renderConversation 只写 Content，ToolCalls → `"Assistant: \n"`） | 渲染配对格式 `[Tool] name(args) → result`（§3.3.1） |
| 2 | 组装侧丢 StepsJSON | agent.go:1212（internal 摘要落库）→ chat_store.go:348（读 StepsJSON）→ context_budget.go:169/175（转换只取 Role/Content） | **不重建 StepsJSON**（R5）；依赖 internal 工具摘要消息原文 + splitRounds 归轮（§3.2） |
| 3 | 组装侧无配对/无最近 N 对 | context_budget.go:140（全量窗口）、:185-188（逐条丢最老） | splitRounds 整轮截断 + 保最近 N 对（§3.2.2/3.2.4） |
| 4 | 两端摘要不落库 | compaction.go:281-308、context_budget.go:213-215（摘要只拼当次 prompt） | 共享压缩摘要存储（§3.1），组装侧读写、循环侧消费 |

---

## 5. 业界对齐

### 5.1 工具数据怎么读（配对渲染）

- **LangGraph / LangMem `SummarizationNode`**：`state["messages"]` 中带 `tool_calls` 的 AI 消息与对应 `ToolMessage` 作为配对单元整体进入压缩批次；压缩边界切到工具调用时连带压掉后续工具结果（"If the last message within `max_tokens_before_summary` is an AI message with tool calls, all of the subsequent, corresponding tool messages will be summarized as well"）。压缩 LLM 看到的工具内容 = name + arguments + result content 文本，无 raw output。
- **配对是协议硬约束**：tool_call 后缺 tool result 请求被拒（`The following tool_call_ids did not have response messages`）——配对原子性非风格偏好。
- **我们的对齐**：循环侧 `groupMessages` 已配对（✅ 半对齐）；组装侧无配对 → §3.2 splitRounds（⚠️→✅）；渲染丢参数 → §3.3.1 配对格式（❌→✅）。

### 5.2 工具数据长如何防污染

| 业界防线 | 机制 | 我们的对齐 |
|---------|------|-----------|
| 源头压缩 | 进入上下文前脱敏+截断 | ✅ 已对齐：`ToolResultGuard`（800 rune Summary，tool_result_guard.go:49-52）、internal 摘要 3000 rune 截断 |
| 预算内截断 | `max_tokens_before_summary` 超预算只压段内（LangGraph） | ⚠️ 半对齐：`ComputeBudget` 四配额账本已有，需把「窗口=未压缩轮数」接入 |
| 分层存储按需拉取 | 工具结果不进核心上下文，落 archival/recall，主动查询才 rehydrate（MemGPT/Letta） | ❌ **不采纳**：属记忆检索职责（`memory_summaries`/`SearchRelevant` 已承担），不在压缩链路职责内；internal 摘要已源头截断 |

### 5.3 五模式对照总表

| 业界模式 | 现状 | 对齐动作 |
|---------|------|---------|
| 源头压缩（脱敏先行） | ✅ 已对齐 | 保持 |
| 配对原子 | ⚠️ 循环侧有、组装侧无 | §3.2 splitRounds |
| 分层渐退 keep 最近 N 对 | ⚠️ 循环侧 `recentGroups` 有、组装侧无 | §3.2.4 |
| 渲染配对格式 | ❌ 未对齐 | §3.3.1 |
| 结构化摘要 schema | ❌ 未对齐 | §3.4 |
| 摘要持久化复用（LangGraph checkpointer / LangMem getSummary-setSummary / Zep / Anthropic prompt caching / AgentScope） | ❌ 摘要不落库 | §3.1 CompactionSummaryStore |

---

## 6. 影响面

### 6.1 改动文件

| 文件 | 改动 |
|------|------|
| `internal/agent/application/context_budget.go` | 游标接入、splitRounds 轮划分、整轮窗口溢出、保最近 N 对、回写 |
| `internal/agent/application/graph/compaction.go` | 摘要 schema 对齐 prompt、渲染配对输入（若有差异） |
| `internal/agent/infrastructure/capability/history_compactor.go` | `renderConversation` 工具对渲染分支 |
| `internal/agent/application/agent.go` | 组装侧注入 CompactionSummaryStore（复用 `ec.historyCompactor` 装配点 agent.go:612/940） |
| `internal/agent/infrastructure/persistence/chat_store.go` | `ListMessages` 需暴露消息 id 供游标锚定（若 `ChatMessage` 未携带 id 则补充） |
| `internal/agent/domain/port/` | 新增 `CompactionStore` port；`CompactionSummary`/`CompactionSegment` 领域类型 |
| `pkg/storage/postgres/tenant_schema.sql` | `chat_compaction_summaries` 建表 + 索引 |

### 6.2 新文件

| 文件 | 内容 |
|------|------|
| `internal/agent/infrastructure/persistence/compaction_store.go` | `CompactionSummaryStore` 实现（execTenant、GetCoverage、Upsert） |
| `internal/agent/application/context_split.go`（或并入 context_budget.go） | `splitRounds` 轮划分 + 配对原子组 |
| 对应测试文件 | 见 §7 |

### 6.3 不变项

- `ToolResultGuard`（源头脱敏）与 `GuardedToolResult` 契约
- `ComputeBudget` 四配额账本、`HistoryCompactor` 接口签名（`messages 按时间正序、仅 user/assistant 轮次` 契约语义随渲染格式扩展，不改签名）
- `memory_summaries`/`HistoryWorker`（记忆语义独立，D7）
- 循环侧 `groupMessages`/`markAnchors`/`flatten`/`toEstimate` 结构、`TokenCorrection`、冷却
- `agent.compaction_prompt` 平台参数（schema 各节对齐它，运维在平台参数页配置全文）
- `steps_json` 列（本方案不新增写入；独立后续任务）

---

## 7. 测试与成功标准

### 7.1 单元测试（表驱动）

| 用例 | 断言 |
|------|------|
| 窗口按未压缩轮数 | 已覆盖 40 轮 + 新增 10 轮 = 窗口 50 不溢出；已覆盖 40 + 新增 15 = 溢出压缩最老轮 |
| 同源消息不重压 | 同 `conversation_id` 二次组装，`GetCoverage` 命中后不触发 compactor（mock 断言调用次数 0） |
| 游标单调推进 | 二次压缩后 `covered_until` > 首次，`version++` |
| 配对原子 | 溢出边界落在 tool 对中间 → 整对保留或整对压掉，不产生孤儿 tool 消息 |
| 最近 N 对保护 | 溢出后最近 N 个工具轮原文保留，仅更老段进压缩 |
| 摘要放 system | 压缩摘要出现在 `systemFull`，不出现在 history 段 |
| 渲染配对格式 | `renderConversation` 对 ToolCalls 消息输出 `[Tool] name(args) → result`；对空 Content 不再输出 `"Assistant: \n"` |
| 防二次注入 | 压缩输入不含 `RawResult`/`StructuredContent`；`sensitiveStructuredKey` 命中项为 `[REDACTED]` |
| fail closed | `GetCoverage` 返回错误 → 降级全量路径；回写失败 → 返回错误暴露，不伪成功 |

### 7.2 集成与 E2E

- 会话 >50 轮，观察第二/三轮请求不再对同源最老段触发 LLM 压缩（通过压缩调用计数或 DB `chat_compaction_summaries` 行确认）。
- 工具调用对在多轮压缩后仍可被后续轮推理（配对 + 最近 N 对保真 + 摘要工具因果链）。
- 前端渲染不受影响（internal 摘要消息 `SkipOutbox` 保持）。
- `api/http/contract_test.go` 与 `.golden.json` 无 diff（本方案不改 HTTP JSON 契约）。
- 成功标准：`make code-quality` 门禁（新函数圈复杂度 ≤10、行数 ≤120、嵌套 ≤4）；`go test -v -race ./...` 全绿。

---

## 8. 参考

- LangGraph/LangMem SummarizationNode 工具配对处理：<https://langchain-ai.github.io/langmem/guides/summarization/>
- LangChain 论坛「tool_call_ids did not have response messages」配对硬约束：<https://forum.langchain.com/t/summarizationnode/522>
- MemGPT/Letta 分层记忆（archival/recall 按需拉取）：<https://zby.github.io/commonplace/agent-memory-systems/reviews/letta/>
- 分层压缩综述（Layers of Memory, Layers of Compression）：<https://timkellogg.me/blog/2025/06/15/compression>
- 既有设计格式参照：`docs/superpowers/specs/2026-08-11-context-window-management-design.md`

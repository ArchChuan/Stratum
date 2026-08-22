# 内置重排改造为 LLM 语义重排设计（workspace 显式模型配置）

日期：2026-08-22
状态：设计定稿（4 份 review 交叉验证完毕，C/I/M 全部纳入；v1 已实现并部署，本版将模型配置从平台级 env 迁移到 workspace 显式配置）
范围：`proto/knowledge/rag.proto`、`internal/knowledge/domain/workspace.go`、`internal/knowledge/application/{rag_service.go,workspace_service.go,evidence_gate.go}`、`internal/knowledge/infrastructure/persistence/workspace_repo.go`、`api/http/handler/rag_handler.go`、`api/middleware/error_mapping.go`、`api/wiring/{knowledge.go,llm_reranker.go,knowledge_judge.go}`、`config/config.go`、`pkg/constants/knowledge.go`、`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`、`web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts`。

> **范围修订说明（4 份 review 交叉验证）**：`rag_handler.go` 的 `toDTOConfig`/`fromDTOConfig` 是 WorkspaceConfig 进出 HTTP 边界的唯一手写映射，不更新则 `rerank_model`/`judge_model` 在创建/更新/读取三处被静默丢弃（前端回填断裂 + 保存失效），是**唯一阻断合并的问题**；`error_mapping.go` 不注册三个新错误会回落 500 而非 400；`useKnowledgeDetailPage.ts` 的 `fetchStats` 回填与 `handleConfigSave` payload 逐字段手拼，不更新则表单值到不了 PATCH。契约 golden 经核实**无需改动**（当前 knowledge golden 均为 401 用例、无 WorkspaceConfig 序列化，见 §7）。

## 1. 背景与问题

RAG 检索流水线为：召回（vector / hybrid / keyword）→ 重排（rerank）→ 阈值过滤 → topK 截断。

内置重排策略 `builtin-score-v1` 当前实现（`rag_service.go` `rerankSources`）只是对候选池按 `Score` 降序做一次稳定排序。但所有到达 `rerankSources` 的池子已经按分数降序（vector 腿 Milvus 按距离升序、hybrid 腿 `rrfFuse` 已按融合分数降序），因此 **builtin 分支是确定性 no-op**：排序不改变任何结果顺序，不产生语义增量。

设计标准：**重排必须基于语义理解产生语义增量**。基于该标准与以下事实决策：

- 不引入 cross-encoder（用户决策："先不引入 cross-encoder，把内置重排用 LLM 网关解决，支持配置重排模型"）。
- 复用平台已有的 LLM 网关能力（`LLMGateway.Gateway` / `LLMCompleter.Complete`），与 `knowledgeJudge`（证据充分性 judge）同一先例。
- 数据外发走平台已有 chat provider（Qwen / Zhipu），信任边界与普通 chat 一致，无新第三方。

v1 已实现平台级 env 配置（`KNOWLEDGE_RERANK_MODEL` / `KNOWLEDGE_JUDGE_*`）并部署生产。**本版是配置层重构**：模型配置不再走 env，而是与 `embedding_model` 同构、逐知识库显式配置，贯彻"模型配置属于 DB 模型治理，不属于 env"的既定原则。

### 已确认的决策（用户裁决）

1. **配置层级**：两个模型（judge + rerank）都必须显式配置在 workspace 配置中（`rerank_model` / `judge_model`），无 env、无 Recommended 兜底。空 = 关闭对应能力（`reranking` 非 builtin 时 `rerank_model` 不适用；`judge_model` 空 = judge 门关闭）。
2. **显式拒绝**：`reranking=builtin-score-v1` 但 workspace 没有 `rerank_model` 时，保存/更新必须返回验证错误（仿 `ErrEmbeddingModelRequired`），不自动降级、不静默兜底。
3. **失败语义**：
   - rerank **fail-open** —— LLM 重排调用失败 / 超时 / 解析失败 → WARN + degraded 指标 → 降级为按召回分数排序，检索永不因重排失败。
   - judge **fail-closed** —— judge 未装配 / 调用失败 / 超时 → WARN + 放行，行为与不配置时一致，绝不误杀检索。
4. **重排范围**：仅对 top-N 精排 —— 先按召回分数取前 N 条（`RerankLLMTopN=10`，与池大小取 min），再做一次 listwise 打分。
5. **模型来源唯一**：模型从 workspace 配置解析，经 `port.ModelExists`（capability `CapChat`）校验在 enabled chat 目录；不存在 → 保存被拒（仿 embedding_model 先例）。

### judge 门：为什么存在（证据充分性门）

judge 门是 v1 引入的生成前门，本版只把它的模型配置迁到 workspace（`judge_model`），语义不变。它解决的是相似度阈值覆盖不到的失败模式：

- **相似度阈值回答"像不像"，judge 回答"能不能推出结论"**，两者正交。阈值过滤低相似度块；judge 处理"相似度高但语义对不上"的证据——例如问"多租户数据隔离怎么做"，知识库里只有一段技术架构文档提到"数据隔离"（向量/关键词都命中，相似度高），但它没有回答操作步骤。此时若基于这段证据生成，模型会**编造**答案，这是 RAG 幻觉的主要来源。
- **判 INSUFFICIENT 会怎样**（`evidence_gate.go:33-37`）：Sources 置空 + `NoAnswer=insufficient_evidence`，主链路直接走"证据不足，无法回答"，绝不基于无关证据生成。**fail-closed**：未装配 / workspace 未配 `judge_model` / 调用失败 / 超时 → WARN + 原样放行，行为与不配置时一致，绝不误杀检索（rerank 是 fail-open 降级排序、judge 是 fail-closed 放行，方向不同是因为 rerank 降级不产生错误答案，而 judge 一旦误判会砍掉本可回答的问题）。
- **业界对照**：有同构机制但非主流框架标准件——CRAG（2024，检索质量评估器，评估 correct/incorrect/ambiguous）最接近但它是"纠正检索"而非"拒绝回答"；Self-RAG 是"按需检索"；groundedness 校验（Cohere / LlamaIndex citation）是**事后**校验答案是否有依据，而我们提前到**生成前**。结论：judge 门是"知识库问答宁可不答也不胡编"的产品决策，与外部重排 / embedding 先例同属工程合理设计。

## 2. 现状代码走查

### 2.1 数据量

| 模式 | 召回池（候选） | 最终给主链路 |
|---|---|---|
| `vector` | `TopK` 条（默认 5） | 阈值过滤后 ≤ TopK |
| `hybrid` | 每腿 `TopK × 2` = 10，RRF 融合去重后 ≤ 20 | 阈值过滤后 ≤ TopK |
| `keyword` | `TopK` 条 | ≤ TopK（无分数无阈值） |

- `DefaultRAGTopK = 5`；外部重排（`cohere:xxx`）时 `RerankWidenFactor = 4` 拓宽召回。
- `rerankSources` 收尾（`rag_service.go:1010-1017`）：**阈值过滤之后再截断**，`len(pool) > rerankTopK(req)`（=`RerankTopK`，0 时用 `TopK`）才截。
- **最终条数 = min(阈值过滤后池子, TopK)**；召回池不足 TopK 时全部返回，不凑满。

### 2.2 关键位置

```
vector/hybrid 召回 → pool（已按分数降序）
  → rerankSources：builtin-score-v1 分支（重排插入点）
  → ScoreThreshold 过滤 → topK 截断 → 主链路（≤ TopK 条）
```

- `rerankSources`（`rag_service.go:1007`）：重排分派核心；`builtin-score-v1` 分支在 `rerankSemantic`（1105-1136，`Model: ""` 空哨兵）+ `rerankBuiltinSemantic`（1141-1155，fail-open 入口）。
- `searchWorkspace`（`rag_service.go:86`）与 `searchWorkspaceWithEvidence`（1299）都先 `resolveWorkspaceConfig`（113，取 workspace config → 构造 `RAGQueryRequest`）→ `rs.Query`。**workspace config 在重排 / judge 调用点前已加载**，新模型字段在此填充进 `RAGQueryRequest`。
- `judgeSufficiencyGate`（`evidence_gate.go:17`）：仅 evidence 路径挂载（`searchWorkspaceWithEvidence` 1324 调用）。判 INSUFFICIENT → `RAGQueryResult{NoAnswer: insufficient_evidence}`（Sources 置空，维持 `content=="" ⇒ NoAnswer!=nil` 不变量）。fail-closed。
- `rerankExternal`（1052）：Cohere 先例 —— `s.Score = r.Score` 覆盖、池收敛、`MinRerankCandidates=3`、`RerankMaxCandidates≤50`。
- `knowledgeJudge`（`api/wiring/knowledge_judge.go:15`）：LLM 判别先例 —— `context.WithTimeout`、`ResponseFormat json_object`、`MaxTokens`、`truncateRunes`。
- `llmReranker`（`api/wiring/llm_reranker.go:22`）：listwise 打分，`Temperature` 显式 0；**`model` 是构造期固定字段（`r.model`，第 68 行 `Model: r.model`）** —— 本版改为从 `req.Model` 读取。
- `LLMCompleter`（`internal/llmgateway/domain/llm.go`）：`Complete(ctx, *CompletionRequest) (*CompletionResponse, error)`。

### 2.3 模型治理现状（DB，非 env）

- `ModelRegistry`（`internal/llmgateway/infrastructure/model_registry.go`）：Provider/Model 存 DB，前端模型管理 UI 维护；`ListChatModelsByTenant` / `ListEmbeddingModelsByTenant` / `ListRerankModelsByTenant`；capability：`CapChat` / `CapEmbedding` / `CapRerank`。
- `knowledgeport.ModelExists`（`model_exists.go:18`）：`Exists(ctx, model string, capability port.ModelCapability) (bool, error)`；`WorkspaceService.SetModelExists` 注入，保存时校验 `embedding_model`（`workspace_service.go:125` → `ErrInvalidEmbeddingModel`）。

### 2.4 待清理的 env 模型配置（本版全部删除）

| env | 配置对象 | 去向 |
|---|---|---|
| `KNOWLEDGE_RERANK_MODEL` / `_TIMEOUT_SECONDS` / `_TOPN` | `KnowledgeRerankConfig` | 删除；`rerank_model` 入 workspace，timeout/topN 用常量 |
| `KNOWLEDGE_JUDGE_ENABLED` / `_MODEL` / `_TIMEOUT_SECONDS` | `KnowledgeJudgeConfig` | 删除；`judge_model` 入 workspace，timeout 用常量 |
| `AGENT_FACTCHECK_ENABLED` / `_JUDGE_MODEL` / `_TOPK` / `_MAX_CLAIMS` | `AgentFactCheckConfig`（agent 输出幻觉校验，advisory） | **保留不变**（agent 域，不在本次范围） |

> 注（review 核实）：`AgentFactCheckConfig` 现为**死配置**——仅被 `config.go` 加载，从未被消费，真实 factcheck 由平台参数 `agent.factcheck.*` 驱动（`api/wiring/agent.go:519-560`）。与 `KnowledgeJudgeConfig` 字段无交集、无共享状态。本次不动，留作独立清理任务；后续读者不应误以为它仍在生效。

## 3. 目标与非目标

### 目标

1. `builtin-score-v1` 产生语义增量：基于 LLM 语义对候选重新排序。
2. **重排 / judge 模型逐知识库显式配置**，从 enabled chat 目录选择，无 env、无平台兜底。
3. `reranking=builtin-score-v1` 未配 `rerank_model` → 保存时显式拒绝（仿 `ErrEmbeddingModelRequired`）。
4. fail-open / fail-closed 失败语义不变：rerank 降级排序、judge 放行，均 WARN 留痕 + 指标。
5. 阈值过滤 / topK 截断 / 校准统计链路不变。
6. 不引入新第三方依赖；删除全部 knowledge 域模型 env。

### 非目标

- 不引入 cross-encoder。
- 不改变外部重排（`cohere:xxx`）行为。
- 不动 `AgentFactCheckConfig`（agent 域幻觉校验）。
- 不做多模型 listwise 组合 / LLM 交叉对比排序。
- 不新增 HTTP API / DTO 契约破坏（proto 字段为 additive 追加）。

## 4. 设计

### 4.1 WorkspaceConfig 新增字段（domain + proto + JSONB）

`internal/knowledge/domain/workspace.go` `WorkspaceConfig` 新增：

```go
RerankModel string // builtin-score-v1 的 LLM 语义重排模型（chat 目录）；空 = 该策略不适用
JudgeModel  string // evidence 路径证据充分性 judge 模型（chat 目录）；空 = judge 门关闭
```

`proto/knowledge/rag.proto` `WorkspaceConfig` 追加（additive，字段 1-9 不动）：

```proto
string rerank_model = 10; // builtin-score-v1 语义重排模型（chat 目录）
string judge_model  = 11; // evidence 路径充分性 judge 模型（chat 目录），空 = 关闭
```

`internal/knowledge/infrastructure/persistence/workspace_repo.go` `jsonbConfig` 追加 `json:"rerank_model"` / `json:"judge_model"`，`toJSONB` / `fromJSONB` 同步映射（仿 `embedding_model`）。

- **JSONB tag 不带 `omitempty`**（对齐 `embedding_model`，与 `Reranking` 等带 omitempty 的字段不同）：部署后新保存的 JSON 会含 `"rerank_model":""`，使 §6 迁移谓词 `config->>'rerank_model' IS NULL` 能可靠区分「部署前旧行（无 key）」与「部署后新行（有 key）」。若用 omitempty，部署后空模型行也缺 key，迁移谓词会误伤。读侧无影响（缺 key → 零值 `""`，符合「空 = 关闭」）。
- **改 proto 后必须执行 `make proto-gen`**：`proto/knowledge/rag.proto` 是参数契约唯一事实源，改字段 10/11 后重新生成 `api/http/dto/gen/` 与 `web/src/services/gen/`（生成物不入 git）。绕过 make 直敲 `go test` 会因生成物未刷新而 import 编译失败，属预期约束。

### 4.2 domain 校验（显式拒绝）与显式清空

`WorkspaceConfig.Validate`（`workspace.go:129`）在 `ErrEmbeddingModelRequired` 检查**之后**追加（保持 embedding 缺失优先——双缺省 `reranking=builtin && RerankModel=="" && EmbeddingModel==""` 时仍返回 `ErrEmbeddingModelRequired`，不改变既有错误优先级）：

```go
// 现有 Validate 末尾（ErrEmbeddingModelRequired 之后）
if c.Reranking == "builtin-score-v1" && c.RerankModel == "" {
    return ErrRerankModelRequired
}
```

- 新增 `ErrRerankModelRequired`（`workspace.go:16` 附近，与 `ErrEmbeddingModelRequired` 同形）。
- **只有 `reranking=builtin-score-v1` 才要求 `rerank_model`**：`reranking=""`（关闭）或外部 `provider:model` 时不适用。judge_model 不强制（空 = 关闭 judge 门，可选增强）。
- 与 embedding 同构：`Validate` 是纯结构检查，目录存在性校验在 application 层（见 §4.3）。

#### 显式清空通道（review C-3 / 数据 C-1：字符串字段零值 = 未传）

PATCH 是 partial 合并（`MergeUpdate` 以零值 = 未提供），新增的 string 字段若照搬 `if partial.X != ""` 模式，则**前端清空模型永远无法持久化**：`judge_model` 一旦配置就关不掉（`""` 被当未传而保留旧值），与「judge_model 空 = 关闭判断门」的可切换语义冲突；`reranking` 的「关闭」选项（提交 `""`）同样被忽略（既有 bug）。修复沿用 `ScoreThresholdResetSentinel`（`workspace.go:100-105`）先例，为字符串字段增加内存瞬态哨兵：

```go
// domain（与 ScoreThresholdResetSentinel 同形，仅存在于内存转换瞬间，绝不落库）
const (
    RerankingResetSentinel  string = "\x00rerank_reset" // handler 检测 PATCH 显式空 reranking
    RerankModelResetSentinel string = "\x00rerank_model_reset"
    JudgeModelResetSentinel  string = "\x00judge_model_reset"
)
```

- **handler 侧编码**（`rag_handler.go` PATCH）：`ShouldBindJSON` 后，从原始请求体解析 `config` 对象，检测 `reranking` / `rerank_model` / `judge_model` 三个 key 是否**显式出现且值为空串**（`c.ShouldBindJSON` 会消费 body，需先 `io.ReadAll` + 重新放回 `c.Request.Body`，或解析原始字节）；显式空 → 在 `fromDTOConfig` 结果上把对应字段设为 sentinel。
- **domain 侧还原**（`MergeUpdate` / `applyRerankSettings`）：哨兵 → 写回 `""`；其余值按现有 partial 语义更新。与 embedding（不可变）无关。
- 这样前端 `allowClear` 清空 judge_model → PATCH 显式空 → 哨兵 → 写回 `""` → judge 门关闭；「关闭」内置重排 → `reranking=""` 显式空 → 哨兵 → 写回 `""`。三者都可逆。
- §8 补「清空模型持久化生效」用例。

### 4.3 application 层目录校验（仿 embedding 先例，create/update 统一）

`validateModelsInCatalogue`（`workspace_service.go:106`）在 **create（:125）与 update（:213）两条路径都已调用**——把新校验放这里即可统一覆盖，避免 update 路径绕过 `ErrRerankModelRequired`（`MergeUpdate` 不调 `Validate`）。在函数开头（`modelExists == nil` 判断**之前**，不依赖注入）追加：

```go
func (s *WorkspaceService) validateModelsInCatalogue(ctx context.Context, cfg domain.WorkspaceConfig) error {
    // create/update 统一：builtin 重排必须配模型（结构校验，不依赖 modelExists）
    if cfg.Reranking == "builtin-score-v1" && cfg.RerankModel == "" {
        return domain.ErrRerankModelRequired
    }
    if s.modelExists == nil {
        return nil
    }
    // ... 既有 embedding 校验 ...
    // 新增：builtin 重排 / judge 模型须在 enabled chat 目录（仿 embedding 先例的
    // err→传播包装、!ok→sentinel 错误 两分支语义）
    if cfg.Reranking == "builtin-score-v1" {
        if ok, err := s.modelExists.Exists(ctx, cfg.RerankModel, port.CapChat); err != nil {
            return fmt.Errorf("knowledge workspace: check rerank model %q: %w", cfg.RerankModel, err) // 目录查询失败 → 传播（5xx），不折叠为 4xx
        } else if !ok {
            return domain.ErrInvalidRerankModel
        }
    }
    if cfg.JudgeModel != "" {
        if ok, err := s.modelExists.Exists(ctx, cfg.JudgeModel, port.CapChat); err != nil {
            return fmt.Errorf("knowledge workspace: check judge model %q: %w", cfg.JudgeModel, err)
        } else if !ok {
            return domain.ErrInvalidJudgeModel
        }
    }
    return nil
}
```

- **knowledge port 需新增 `CapChat`**：`internal/knowledge/domain/port/model_exists.go` 现有 `CapEmbedding` / `CapRerank`，无 `CapChat`。新增 `CapChat ModelCapability = "chat"`，并在 `knowledgeModelExistsAdapter.Exists`（`api/wiring/knowledge.go:338`）的 switch 增加 `case knowledgeport.CapChat: names, err = a.registry.ListChatModelsByTenant(ctx)`（registry 已有该方法）。
- capability 用 `CapChat`：内置重排与 judge 都是 chat 补全（listwise / 判别），不是专用 rerank 模型（Cohere 的 `CapRerank`）。
- **错误传播对齐 embedding 先例**（review I-1）：目录**查询失败**（DB/registry 瞬时故障）向上传播包装错误 → 5xx，仅「模型不在目录」（`!ok`）才返回 `ErrInvalidRerankModel` / `ErrInvalidJudgeModel`（400）。不把基础设施错误折叠成配置错误。
- create 路径：`NewWorkspace` 的 `Validate` 先返回 `ErrRerankModelRequired`（§4.2）；update 路径由此处兜底，两条路径错误码一致。

### 4.4 模型传递进检索（RAGQueryRequest + resolveWorkspaceConfig）

`RAGQueryRequest`（`rag_service.go:253`）新增内部字段（**非 DTO**，无契约影响）：

```go
RerankModel string // 由 resolveWorkspaceConfig 从 workspace config 填充
JudgeModel  string // 同上，evidence 路径 judge 门使用
```

`resolveWorkspaceConfig`（`rag_service.go:113`）返回值追加 `rerankModel, judgeModel string`，从 `w.Config.RerankModel` / `w.Config.JudgeModel` 填充；`searchWorkspace`（86）与 `searchWorkspaceWithEvidence`（1299）构造 `RAGQueryRequest` 时带上。

> **实现提示**：原返回值已 6 个（含 err），追加后 8 个，可读性差。实现时把返回值收敛为结构体（如 `type resolvedWorkspace struct { mode, effectiveTopK, embedModel, workspaceID, rerankModel, judgeModel string; threshold float32 }`），两个调用点同步适配。

**`rerankSemantic`**（`rag_service.go:1105`）把空哨兵 `Model: ""` 改为：

```go
results, err := rs.semanticReranker.Rerank(ctx, knowledgeport.RerankRequest{
    Query: req.Question, Documents: docs, Model: req.RerankModel, TopN: topN,
})
```

**`llmReranker`**（`api/wiring/llm_reranker.go:22`）移除构造期 `model` 字段，`Rerank` 改用 `req.Model`，**并新增空模型守卫**（review C-1，必须新增，不能依赖网关行为）：

```go
func (r *llmReranker) Rerank(ctx context.Context, req knowledgeport.RerankRequest) ([]knowledgeport.RerankResult, error) {
    if req.Model == "" {
        // 空模型守卫：Gateway.resolveChain（gateway.go:184-195）对空模型会显式回填
        // provider 默认 chat 模型、不报错——若无此守卫，移除装配门后空模型可达的
        // 路径（update 未配模型、遗留 builtin、evaluation 快照）会用错误默认模型
        // 静默重排，比 fail-open 降级更糟（语义错、非确定、无 degraded 留痕）。
        return nil, errors.New("llm rerank: empty model")
    }
    // ...
    Model: req.Model, // 逐 workspace 模型（第 68 行 r.model → req.Model）
}
```

- `newLLMReranker` 签名改为 `newLLMReranker(completer, timeout, metrics, logger)`（移除 model 参数）。
- **触发面（review M-2 明确）**：`searchWorkspace` / `searchWorkspaceWithEvidence` 当前构造的 `RAGQueryRequest` 不设 `Reranking`（`resolveWorkspaceConfig` 不返回 Reranking），builtin 分支实际由 **evaluation 快照路径**（`retrieval_evaluator.go:173`，`Reranking: snapshot.Reranking`）触发。§4.4 将 `RerankModel` 填充到两处检索入口是防御性完备（未来入口开启 builtin 即带模型）；evaluation 快照的模型传递见 §7 裁决（本轮不集成，空模型走守卫 fail-open）。

### 4.5 judge resolver（per-workspace 模型，DDD 合规）

judge 是接口（`SufficiencyJudge.JudgeSufficiency(ctx, query, evidence)`），模型不能像 rerank 那样从请求参数读。采用 **resolver** 模式，application 只消费 domain/port 接口，模型装配留在组合根：

`rag_service.go`：`SetSufficiencyJudge`（单实例注入）改为：

```go
// RAGService 字段
sufficiencyJudgeResolver func(ctx context.Context, model string) (port.SufficiencyJudge, error)

func (rs *RAGService) SetSufficiencyJudgeResolver(
    r func(ctx context.Context, model string) (port.SufficiencyJudge, error),
) { rs.sufficiencyJudgeResolver = r }
```

`evidence_gate.go` `judgeSufficiencyGate` 签名追加 `model string`，逻辑改为：

```go
func (rs *RAGService) judgeSufficiencyGate(ctx context.Context, tenantID, workspace, query, model string, result *RAGQueryResult) *RAGQueryResult {
    if rs.sufficiencyJudgeResolver == nil || model == "" || len(result.Sources) == 0 {
        return result // 未装配 / workspace 未配 judge_model / 无证据 → 不判
    }
    judge, err := rs.sufficiencyJudgeResolver(ctx, model)
    if err != nil || judge == nil {
        rs.logger.Warn("knowledge.judge.sufficiency_degraded",
            zap.String("tenant_id", tenantID), zap.String("workspace", workspace), zap.Error(err))
        return result // fail-closed：解析失败放行，不误杀
    }
    verdict, err := judge.JudgeSufficiency(ctx, query, formatSources(result.Sources))
    if err != nil {
        rs.logger.Warn("knowledge.judge.sufficiency_degraded", /* 同上 */)
        return result
    }
    if verdict != port.SufficiencyInsufficient {
        return result
    }
    // INSUFFICIENT → 置空 Sources + NoAnswer（保持 content=="" ⇒ NoAnswer!=nil 不变量）
    if rs.metrics != nil {
        rs.metrics.IncNoAnswer(tenantID, constants.NoAnswerReasonInsufficientEvidence)
    }
    return &RAGQueryResult{
        NoAnswer:       buildNoAnswer(NoAnswerInsufficientEvidence, result.CandidateCount, 0, result.BestScore),
        BestScore:      result.BestScore,
        CandidateCount: result.CandidateCount,
    }
}
```

调用点（`searchWorkspaceWithEvidence` 1324）传入 `req.JudgeModel`：

```go
out = rs.judgeSufficiencyGate(ctx, tenantID, ws, query, req.JudgeModel, out)
```

**wiring resolver**（`api/wiring/knowledge.go` `wireKnowledgeJudge` 改造）：

```go
func (c *Container) wireKnowledgeJudge(rag *knowledge.RAGService) {
    if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
        return // gateway 不可用 → 不装配（fail-closed 放行）
    }
    rag.SetSufficiencyJudgeResolver(func(_ context.Context, model string) (knowledgeport.SufficiencyJudge, error) {
        if model == "" {
            return nil, nil // workspace 未配 judge_model → 不判
        }
        j := knowledgeJudge{
            completer: c.LLMGateway.Gateway,
            model:     model, // 逐 workspace 模型
            timeout:   constants.KnowledgeJudgeTimeout,
        }
        if c.Platform != nil {
            j.metrics = c.Platform.Metrics
        }
        return j, nil
    })
}
```

- **无缓存**：`knowledgeJudge` 无状态（共享 completer + 动态 model），每次构造廉价；模型目录校验已在保存时完成，resolver 不重复查目录。
- 目录查询失败的 fail-closed 由 §4.3 保存时兜底；运行期 judge 调用失败由 `judgeSufficiencyGate` 放行。

### 4.6 wiring / config 清理（删除全部 knowledge 域模型 env）

`config/config.go`：

- 删除 `KnowledgeRerankConfig` struct 及 `RerankLLMConfigured()`。
- 删除 `KnowledgeJudgeConfig` struct。
- 删除 6 个 env 绑定：`KNOWLEDGE_RERANK_MODEL` / `_TIMEOUT_SECONDS` / `_TOPN`、`KNOWLEDGE_JUDGE_ENABLED` / `_MODEL` / `_TIMEOUT_SECONDS`。
- **保留 `AgentFactCheckConfig`（`AGENT_FACTCHECK_*`）不动**。

`api/wiring/knowledge.go`：

- `semanticRerankerDeps` 简化：删除 `RerankLLMConfigured()` 检查、`llmRerankModelInCatalogue` 调用、`krc.Model` 传参。gateway 可用即注入：

```go
func (c *Container) wireSemanticReranker(rag *knowledge.RAGService) {
    if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
        return
    }
    var metrics observability.MetricsProvider
    if c.Platform != nil {
        metrics = c.Platform.Metrics
    }
    rag.SetSemanticReranker(
        newLLMReranker(c.LLMGateway.Gateway, constants.RerankLLMTimeout, metrics, c.Logger),
        constants.RerankLLMTopN,
    )
}
```

- 删除 `llmRerankModelInCatalogue`（模型目录校验移到保存时，§4.3）。
- `knowledge_judge.go` 不动（struct 保留 `model` 字段，由 resolver 动态填充）。

`pkg/constants/knowledge.go`：`RerankLLMTopN=10`、`RerankLLMMaxTokens=1024`、`RerankLLMTimeout=5s`、`RerankLLMMaxDocRunes=500` 保持不变（行为数字，非模型配置）。

**HTTP 状态映射（review I-1/I-4）**：`api/middleware/error_mapping.go` 的 `errorStatusTable` 追加三个新 sentinel → 400（`MapErrorToStatus` 未命中回落 500，不注册则配置错误以 "internal server error" 呈现、前端引导文案全丢）：

```go
knowledgedomain.ErrRerankModelRequired: http.StatusBadRequest,
knowledgedomain.ErrInvalidRerankModel:   http.StatusBadRequest,
knowledgedomain.ErrInvalidJudgeModel:    http.StatusBadRequest,
```

对齐既有 `ErrInvalidEmbeddingModel: 400` / `ErrEmbeddingModelRequired: 400`（`error_mapping.go:180-181`）。并在 `workspace_service.go` 顶部加 application 层别名（仿 `ErrInvalidEmbeddingModel` / `ErrEmbeddingModelRequired`）。可选增强：仿 `approvalPublicMessage`（`public_error.go:41-60`）为三个错误加中文 public message（`DescribePublicError` 对 4xx 默认返回英文 sentinel 原文）。

### 4.7 前端（WorkspaceConfigForm.tsx + useKnowledgeDetailPage.ts）

新增「重排模型」「判断模型」两个下拉，数据源为 chat 模型目录：

- **重排模型**：仅 `reranking=builtin-score-v1` 时显示/必填（antd `rules` + 后端 `Validate` 双重校验）。tooltip："内置重排使用所选模型对候选语义精排；未配置模型时无法启用内置重排。外部重排需在模型管理中配置"。**该 Form.Item 设 `preserve={false}`**（review M-2）：切回「关闭」时 Field 卸载，避免值残留 form store 污染 payload。
- **判断模型**：可选，antd Select 带 `allowClear`；tooltip："配置后，证据检索会先判断证据能否支撑结论，不足时回答'证据不足'（不胡编）。留空 = 关闭判断门"。清空提交显式空串，后端经哨兵持久化（§4.2）。
- **存量 reranking tooltip 更新**（review I-2）：现文案「未配置时自动降级为分数排序」与「显式拒绝」语义矛盾，改为「内置重排需在模型管理中配置重排模型；未配置模型时无法保存。外部重排需在模型管理中配置」。
- **`WorkspaceConfigForm` 新增 `chatModels: string[]` prop**，由编辑页 hook 注入。
- **`useKnowledgeDetailPage.ts`（review C-2，两处断链）**：
  - `fetchStats` 回填映射追加 `rerank_model` / `judge_model`（响应侧由 `toDTOConfig` 输出，§范围修订）；
  - `handleConfigSave` 构建的 PATCH payload 追加 `rerank_model` / `judge_model`（当前逐字段手拼，缺两字段则表单值到不了 PATCH）；
  - detail hook 并行拉 `llmApi.getCatalogue()`（`web/src/modules/llm/api/llm.api.ts:39-45`，返回 `chatModels: string[]`），`chatModels` 经 prop 传入表单（列表页 `useKnowledgePage.ts:46` 已有先例）。
- **`ConfigValues` 接口收敛**（review C-2）：`WorkspaceConfigForm.tsx:19` 与 `useKnowledgeDetailPage.ts:21` 两处重复定义，统一为单一共享类型（或至少两处同步），追加 `rerank_model` / `judge_model`；`workspaceConfigSchema`（`web/src/modules/knowledge/model/knowledge.ts:3-15`）显式声明两字段（当前 `.passthrough()` 会透传但类型无声明）。
- 后端保存失败（`ErrRerankModelRequired` / `ErrInvalidRerankModel` / `ErrInvalidJudgeModel`）统一走现有 `message.error` 约定，错误正文由 handler 冻结的 `{"error":"..."}` 给出（§4.8 注册 → 400 后前端 `extractErrorMessage` 能拿到引导文案）。

### 4.8 数据流（最终形态）

```
保存 workspace：reranking=builtin-score-v1 且无 rerank_model → 拒绝（ErrRerankModelRequired，create/update 统一）
                rerank_model / judge_model 非空 → Exists(_, CapChat) 校验，无效 → 拒绝（ErrInvalidRerankModel/JudgeModel）；目录查询失败 → 传播（5xx）
                PATCH 显式空串（前端 allowClear/关闭）→ 哨兵 → 写回 ""（清空持久化）
                三个新错误 → errorStatusTable 400 → 前端 message.error 展示引导文案

查询（vector/hybrid）：
  resolveWorkspaceConfig → RAGQueryRequest{RerankModel, JudgeModel} 填充
  → pool（召回分数降序）
  → [builtin 分支] req.RerankModel → RerankRequest.Model → LLM listwise 打分
     → 覆盖 Score → 池收敛 min(RerankLLMTopN, len(pool))
     → 降级路径：模型为空/调用失败/超时 → WARN + degraded → 召回分数排序
  → ScoreThreshold 过滤（基于 LLM 分数）→ topK 截断 → 主链路 ≤ TopK 条

evidence 路径（searchWorkspaceWithEvidence）：
  → judgeSufficiencyGate(req.JudgeModel)
     → resolver 解析 judge → JudgeSufficiency
     → INSUFFICIENT → Sources 置空 + NoAnswer=insufficient_evidence
     → 未装配/空模型/失败/超时 → WARN + 放行
```

## 5. 错误处理与降级

- **保存拒绝（显式）**：`reranking=builtin-score-v1` 无 `rerank_model` → `ErrRerankModelRequired`；模型不在 chat 目录 / 目录查询失败 → `ErrInvalidRerankModel` / `ErrInvalidJudgeModel`。均不自动降级。
- **rerank 调用失败 / 超时 / 解析失败**：`rerankSemantic` 返回 error → WARN `knowledge.retrieval.llm_rerank_degraded`（含 `pool_size` / `top_n`，不含 query/chunk/response 原文）→ `IncRerankRequest(..., "degraded")` → 池不变 → 召回分数排序。检索不失败。
- **`req.Model == ""`（显式守卫，§4.4）**：`llmReranker.Rerank` 顶部返回 error → 走 fail-open 降级（正常路径 `Validate`/`validateModelsInCatalogue` 已保证 builtin 有模型；守卫防「移除装配门后可达的空模型路径」——evaluation 快照、遗留数据——被网关回填默认模型静默错误重排）。
- **`len(pool) < 2` / `topN < 2`**：跳过 LLM，直接排序。
- **judge 未装配 / workspace 未配 `judge_model` / 解析失败 / 调用失败 / 超时**：WARN `knowledge.judge.sufficiency_degraded` → 放行，行为与不配置一致。
- **日志红线**：降级 / 失败日志只记录 tenant / workspace / model 名 / error 摘要，**禁止记录 query / chunk / response 原文**；重排与 judge 失败不落任何检索正文。

## 6. 迁移

存量 tenant 的 workspace 若 `reranking=builtin-score-v1` 且无 `rerank_model`，升级后保存会被 `Validate` 拒绝（§4.2/§4.3）。迁移策略：

1. **推荐**：一次性运维 SQL（tenant-only 表 **`rag_workspaces`**，见 `pkg/storage/postgres/tenant_schema.sql:677-692`）——把 `reranking="builtin-score-v1"` 且无 `rerank_model` 的 workspace 的 `reranking` 置空（`""`）。新实现下无模型 builtin 本就是降级排序，清空为「关闭重排」语义等价，保存不再被拒。

   `rag_workspaces` 是 **tenant-only 表**：`pkg/migration/sql/` 编号迁移只操作 public schema、禁止引用 tenant-only 表，且 `ProvisionAllTenantSchemas` 只幂等应用模板 DDL、不做数据 UPDATE——**没有现成代码通道**，必须作为运维动作按 `tenant_<id>` 逐 schema 执行。幂等 SQL（JSONB `||` 整体替换 `reranking`，重复执行无副作用）：

   ```sql
   UPDATE tenant_<id>.rag_workspaces
   SET config = config || '{"reranking":""}'
   WHERE config->>'reranking' = 'builtin-score-v1'
     AND (config->>'rerank_model' IS NULL OR config->>'rerank_model' = '');
   ```

   执行脚本：运维脚本 `scripts/knowledge-rerank-workspaces-migration.sh` 已实现逐 tenant schema 迁移——按 `SELECT id FROM public.tenants WHERE deleted_at IS NULL` 枚举全部 tenant（含历史租户），对每个 schema 跑上述 UPDATE 并把 `tenant_<id>` 替换为实际 schema 名；以 `RETURNING` + `wc -l` 报告每库影响行数以便对账（幂等，可重复执行）。

   **部署排序（关键）**：§4.2 校验上线后，受影响 workspace 的任何保存都会被拒。迁移 SQL 必须在代码部署**之前/同时**执行，否则存在「迁移前保存被拒」窗口。远端生产写入按项目规则先获用户许可。

2. 备选：管理员在模型管理配置模型后逐库保存补全。
3. 启动路径检测：`knowledge` 启动时不拦截（fail-open 原则）；在 `workspace` 列表/编辑接口返回时，前端对 builtin 无模型的 workspace 显示引导文案（"内置重排需配置重排模型"），不阻塞其他操作。

> 迁移谓词依赖 §4.1 的「JSONB 不带 omitempty」：部署后新保存的行会含 `"rerank_model":""`（有 key），旧行无 key（`IS NULL`）——谓词据此区分。若带 omitempty，部署后空模型行也缺 key，谓词无法区分，故必须不带 omitempty。
> 生产现状：`KNOWLEDGE_RERANK_MODEL` 生产未配置 → 语义重排实际关闭（fail-open 降级排序）。迁移后由各 workspace 显式启用。

## 7. 评估影响

- evaluation baseline 默认 `Reranking=RerankingNone`（`evaluation_knowledge_adapter.go:24` `knowledgeDefaultReranking`），不触发语义重排，行为不变。
- **显式 `builtin-score-v1` 快照本轮不集成语义重排**（review I-3 裁决）：`RetrievalSnapshot`（`retrieval_evaluator.go:34`）与 wiring 层 `knowledgeRetrievalSnapshot`（`evaluation_knowledge_adapter.go:40`）都无 `RerankModel` 字段，evaluation 构造的请求 `req.RerankModel` 恒空 → 走 §4.4 空模型守卫 fail-open 降级为召回分数排序（不产生错误结果，WARN 一次，评估不阻塞）。**evaluation 快照语义重排集成留作独立任务**（需给 `RetrievalSnapshot` + `knowledgeRetrievalSnapshot` 加 `RerankModel` 并透传），不在本版范围。
- **可复现性**：`Temperature=0` 固定采样（`llm_reranker.go:65` 保持），同一快照跨运行排序确定性提升。不做 `SkipSemanticRerank` 评估旁路——评估应测产品真实行为（本版 evaluation 测的是召回分数排序，即未启用语义重排的行为）。
- `rerankAvailable`（Cohere 装配标志）不变；`rerankStats.poolSize / bestScore` 重排前采集，校准数据不受影响。
- **契约测试（review I-1/I-4，golden 前提修正）**：当前 `api/http/testdata/contracts/` 下全部 knowledge golden（`get/patch/post/put_knowledge_workspaces*` 等）**只含未认证 401 用例**（`{"error":"missing bearer token"}`），无任何 WorkspaceConfig 序列化（grep `embedding_model`/`reranking` 零命中）；契约测试将 `/knowledge/*` 路由到 legacy router（`contract_test.go:161-166`），只会打到 401。因此 **golden 预计无需改动**。DTO 序列化正确性靠新增的 `toDTOConfig`/`fromDTOConfig` 字段保全测试保障（§8），不依赖 golden。

## 8. 测试

1. **domain `Validate`**：`reranking=builtin-score-v1` + 空 `rerank_model` → `ErrRerankModelRequired`；外部 rerank / 空 `reranking` 不要求模型；**双缺省（builtin + 空 rerank_model + 空 embedding_model）→ 仍返回 `ErrEmbeddingModelRequired`**（新检查在 embedding 之后，优先级不变）。
2. **domain `MergeUpdate` 显式清空**：PATCH 传 `RerankingResetSentinel` / `JudgeModelResetSentinel` / `RerankModelResetSentinel` → 合并结果对应字段为 `""`（清空持久化生效）；不传（零值）→ 保留旧值。
3. **application 保存/更新校验**（create + update 两条路径）：builtin + 有效 chat 模型 → 通过；builtin + 空 `rerank_model` → `ErrRerankModelRequired`（**update 路径必须有此用例**——`MergeUpdate` 不调 `Validate`，靠 `validateModelsInCatalogue` 兜底）；模型不在目录 → `ErrInvalidRerankModel`；**目录查询失败 → 传播包装错误（非折叠成 4xx）**；`judge_model` 非空无效 → `ErrInvalidJudgeModel`；`judge_model` 空 → 通过（关闭门）。
4. **`rag_service`**：
   - `resolveWorkspaceConfig` 填充 `RerankModel` / `JudgeModel`；
   - `rerankSemantic` 传 `req.RerankModel`（非空哨兵）到 `RerankRequest.Model`；
   - judge resolver：workspace 配模型 → 判定生效；空模型 → 放行；resolver 失败 → WARN + 放行（fail-closed）；
   - 既有 fail-open / 降级 / topN 与池取 min / 补尾用例保持。
5. **`llmReranker` 空模型守卫**（`api/wiring/llm_reranker_test.go`）：`RerankRequest{Model:""}` → error（fail-open 触发）；非空 → 请求体携带 `Model`。
6. **wiring**：gateway 可用即注入 reranker（不再校验模型）；resolver 动态构造 judge（model 从参数）；gateway nil 不注入。
7. **handler DTO 往返**（新增）：`toDTOConfig` / `fromDTOConfig` 对 `RerankModel` / `JudgeModel` 字段保全（domain → DTO → JSON → DTO → domain 不丢字段）；PATCH 显式空 → sentinel 编码生效。
8. **error_mapping**：三个新 sentinel → 400（`errorStatusTable` 命中）。
9. **受影响测试迁移清单**（review 枚举，实现时必须同步）：
   - `api/wiring/llm_reranker_test.go`：`newLLMRerankerStub`（:77-78）、model 断言（:92、:303-304）、直接调用（:162、:206）共 5 处适配 `newLLMReranker` 无 model 签名；`newSemanticRerankContainer`（:228-253）去掉 `config.KnowledgeRerank` 依赖；`TestSemanticRerankerDepsGates`（:255-321）wiring 期模型 WARN 行为（`knowledge.rerank.model_unavailable`）随 `llmRerankModelInCatalogue` 删除而消失，改由 application `validateModelsInCatalogue(CapChat)` 承接后迁移到 application 层；**`TestLLMRankModelInCatalogue`（:323-345）处置**：删除或改写为应用层 catalogue 校验测试。
   - `internal/knowledge/application/evidence_gate_test.go`：`SetSufficiencyJudge`（:32、:106）→ `SetSufficiencyJudgeResolver(func(ctx, model) (port.SufficiencyJudge, error) { return j, nil })` 形态；覆盖「模型未知时 resolver 返回错误」失败路径。
10. **config**：删除 6 个 env 后 `Load()` 测试更新（`AgentFactCheckConfig` 保留用例）。
11. **契约测试**：golden **无需改动**（§7 已证：现有用例均 401、无 WorkspaceConfig 序列化）。
12. **回归**：`go vet && go test -short ./...`；`make fe-lint && make fe-build`（前端）；代码质量门禁（新函数复杂度 / 行数）。

## 9. 风险与开放点

- **阈值跨分数空间**（同 v1）：LLM 覆盖 Score 后 `ScoreThreshold` 过滤基于 LLM 分数（0-1）；fail-open 降级时回到召回分数空间。与 Cohere 外部重排先例一致，是启用/降级重排的固有语义。`BestScore / CandidateCount` 校准不受影响。
- **存量 workspace 保存拒绝**：§6 迁移 SQL 需覆盖所有 tenant（含历史租户），tenant_schema 与编号迁移的差异（JSONB 无需 DDL）已规避——`rerank_model` / `judge_model` 是 JSONB 内字段，**无新增列，不涉及 tenant DDL / 编号迁移**。
- **judge resolver 每查询构造**：无状态对象，代价可忽略；若未来模型目录校验需在运行期重复，再引入缓存（当前不 YAGNI）。
- **降级日志级别**：配置但失败打 WARN（非 ERROR）——降级期间多工作区扇出下避免 ERROR 洪水；告警由 degraded 指标率驱动。与 v1 裁决一致。
- **token 成本**：单次查询 ≤ `RerankLLMTopN × RerankLLMMaxDocRunes` ≈ 10 × 500 runes 输入 + 输出，量级可控。
- **指标**：rerank 指标标签固定 `builtin-llm`，不暴露模型名（模型现为 workspace 级，更不落日志）。**judge 指标沿用模型名标签**（`knowledge_judge.go:144-148` `IncKnowledgeJudge(j.model, status)`，受目录规模约束、非受 workspace 数约束，基数仍可控；如需收敛再考虑固定标签）——review M-3 确认非正确性问题。
- **模型治理一致性**：embedding_model 走 `ModelExists(CapEmbedding)`，本版 rerank/judge 走 `ModelExists(CapChat)`——能力区分由目录 capability 决定，不做跨能力自动降级。
- **迁移 SQL 不建索引**（review M-3 确认）：§6 迁移 SQL 为逐租户全表扫描，per-tenant `rag_workspaces` 行数小（数十级）且一次性执行，无需在 `config` 上建 GIN 索引；应用层所有读取（`GetByName`/`GetByID`/`List`/`GetConfig*`）都是整列取 `config`，无 JSONB 路径查询，无新索引需求。
- **历史 plan 文档同步**（review M-2）：`docs/superpowers/plans/2026-08-22-llm-rerank.md` 引用 6 个 `KNOWLEDGE_RERANK_*`/`KNOWLEDGE_JUDGE_*` env 与 `KnowledgeRerankConfig`，迁移落地后与代码不一致。收尾时同步更新或归档该 plan 文档，保持仓库唯一事实源。

## 10. 验收标准

1. 保存 workspace：`reranking=builtin-score-v1` 无 `rerank_model` → 返回 `ErrRerankModelRequired`（前端展示引导）。
2. 配置 `rerank_model`（chat 目录内）后，builtin 查询对 top-N 候选产生语义排序，分数为 LLM 分数。
3. LLM 调用失败 / 超时 → WARN + degraded 指标 + 召回分数排序，检索不失败。
4. `judge_model` 配置后 evidence 路径：证据不足 → `NoAnswer=insufficient_evidence`；未配 / 失败 → 放行。
5. 全部 knowledge 域模型 env（6 个）从 `config/config.go` 删除，`AgentFactCheckConfig` 保留。
6. 最终返回条数 ≤ TopK；召回池不足 TopK 时全部返回。
7. 清空 judge_model / 关闭内置重排后保存，后端持久化为空（显式清空通道生效）。
8. `llmReranker.Rerank` 空模型 → error（fail-open 降级），不用默认模型静默重排。
9. `toDTOConfig` / `fromDTOConfig` 对 `RerankModel` / `JudgeModel` 往返保全；前端编辑页两个下拉回填正确、保存 payload 携带两字段。
10. `go test -short ./...` 全绿，`make fe-lint && make fe-build` 通过，代码质量门禁通过；契约 golden 经核实无需改动（§7）。

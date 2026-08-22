# 内置重排改造为 LLM 语义重排设计（workspace 显式模型配置）

日期：2026-08-22
状态：设计修订中（v1 已实现并部署，本版将模型配置从平台级 env 迁移到 workspace 显式配置）
范围：`proto/knowledge/rag.proto`、`internal/knowledge/domain/workspace.go`、`internal/knowledge/application/{rag_service.go,workspace_service.go,evidence_gate.go}`、`internal/knowledge/infrastructure/persistence/workspace_repo.go`、`api/wiring/{knowledge.go,llm_reranker.go,knowledge_judge.go}`、`config/config.go`、`pkg/constants/knowledge.go`、`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`。

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

### 4.2 domain 校验（显式拒绝）

`WorkspaceConfig.Validate`（`workspace.go:129`）在结构检查之后、`ErrEmbeddingModelRequired` 之前追加：

```go
if c.Reranking == "builtin-score-v1" && c.RerankModel == "" {
    return ErrRerankModelRequired
}
```

- 新增 `ErrRerankModelRequired`（`workspace.go:16` 附近，与 `ErrEmbeddingModelRequired` 同形）。
- **只有 `reranking=builtin-score-v1` 才要求 `rerank_model`**：`reranking=""`（关闭）或外部 `provider:model` 时不适用。judge_model 不强制（空 = 关闭 judge 门，可选增强）。
- 与 embedding 同构：`Validate` 是纯结构检查，目录存在性校验在 application 层（见 §4.3）。

### 4.3 application 层目录校验（仿 embedding 先例）

`internal/knowledge/application/workspace_service.go` 复用现有 `port.ModelExists`（`SetModelExists` 已注入），在保存/更新 workspace 的结构校验后追加：

```go
// 仿 ErrInvalidEmbeddingModel 分支（workspace_service.go:125）
if wcfg.Reranking == "builtin-score-v1" {
    ok, err := s.modelExists.Exists(ctx, wcfg.RerankModel, port.CapChat)
    if err != nil { return domain.ErrInvalidRerankModel } // fail-closed：目录查询失败拒绝保存
    if !ok { return domain.ErrInvalidRerankModel }
}
if wcfg.JudgeModel != "" {
    ok, err := s.modelExists.Exists(ctx, wcfg.JudgeModel, port.CapChat)
    if err != nil || !ok { return domain.ErrInvalidJudgeModel }
}
```

- **knowledge port 需新增 `CapChat`**：`internal/knowledge/domain/port/model_exists.go` 现有 `CapEmbedding` / `CapRerank`，无 `CapChat`。新增 `CapChat ModelCapability = "chat"`，并在 `knowledgeModelExistsAdapter.Exists`（`api/wiring/knowledge.go:338`）的 switch 增加 `case knowledgeport.CapChat: names, err = a.registry.ListChatModelsByTenant(ctx)`（registry 已有该方法）。
- capability 用 `CapChat`：内置重排与 judge 都是 chat 补全（listwise / 判别），不是专用 rerank 模型（Cohere 的 `CapRerank`）。
- 错误传播：目录查询失败按"模型无效"拒绝保存（fail-closed），不静默放行。

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

**`llmReranker`**（`api/wiring/llm_reranker.go:22`）移除构造期 `model` 字段，`Rerank` 改用 `req.Model`：

```go
Model: req.Model, // 逐 workspace 模型（第 68 行 r.model → req.Model）
```

- `req.Model == ""` 防御性返回 error（调用方 fail-open 降级）；正常路径 `Validate` 已保证 builtin 有模型。
- `newLLMReranker` 签名改为 `newLLMReranker(completer, timeout, metrics, logger)`。

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

### 4.7 前端（WorkspaceConfigForm.tsx）

新增「重排模型」「判断模型」两个下拉，数据源为 chat 模型目录（复用模型管理 API，`ListChatModelsByTenant`）：

- **重排模型**：仅 `reranking=builtin-score-v1` 时显示/必填（antd `rules` + 后端 `Validate` 双重校验）。tooltip："内置重排使用所选模型对候选语义精排；未配置模型时无法启用内置重排。外部重排需在模型管理中配置"。
- **判断模型**：可选；tooltip："配置后，证据检索会先判断证据能否支撑结论，不足时回答'证据不足'（不胡编）。留空 = 关闭判断门"。
- `ConfigValues` 接口 / 表单回填同步追加 `rerank_model` / `judge_model`。
- 后端保存失败（`ErrRerankModelRequired` / `ErrInvalidRerankModel` / `ErrInvalidJudgeModel`）统一走现有 `message.error` 约定，错误正文由 handler 冻结的 `{"error":"..."}` 给出。

### 4.8 数据流（最终形态）

```
保存 workspace：reranking=builtin-score-v1 且无 rerank_model → 拒绝（ErrRerankModelRequired）
                rerank_model / judge_model 非空 → Exists(_, CapChat) 校验，无效 → 拒绝

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
- **`req.Model == ""`（防御）**：`llmReranker.Rerank` 返回 error → 走降级（正常路径 `Validate` 已保证不触发）。
- **`len(pool) < 2` / `topN < 2`**：跳过 LLM，直接排序。
- **judge 未装配 / workspace 未配 `judge_model` / 解析失败 / 调用失败 / 超时**：WARN `knowledge.judge.sufficiency_degraded` → 放行，行为与不配置一致。
- **日志红线**：降级 / 失败日志只记录 tenant / workspace / model 名 / error 摘要，**禁止记录 query / chunk / response 原文**；重排与 judge 失败不落任何检索正文。

## 6. 迁移

存量 tenant 的 workspace 若 `reranking=builtin-score-v1` 且无 `rerank_model`，升级后保存会被 `Validate` 拒绝。迁移策略：

1. **推荐**：一次性 SQL（tenant 表 `workspaces` JSONB 更新）——把 `reranking="builtin-score-v1"` 且无 `rerank_model` 的 workspace 的 `reranking` 置空（`""`）。新实现下无模型 builtin 本就是降级排序，清空为"关闭重排"语义等价，保存不再被拒。
2. 备选：管理员在模型管理配置模型后逐库保存补全。
3. 启动路径检测：`knowledge` 启动时不拦截（fail-open 原则）；在 `workspace` 列表/编辑接口返回时，前端对 builtin 无模型的 workspace 显示引导文案（"内置重排需配置重排模型"），不阻塞其他操作。

> 生产现状：`KNOWLEDGE_RERANK_MODEL` 生产未配置 → 语义重排实际关闭（fail-open 降级排序）。迁移后由各 workspace 显式启用。

## 7. 评估影响

- evaluation 默认 `RerankingNone`（`retrieval_evaluator.go`），不受影响。
- 显式 `builtin-score-v1` 快照会触发 LLM 重排；失败自动降级，不阻塞评估（fail-open）。
- **可复现性**：`Temperature=0` 固定采样（`llm_reranker.go:65` 保持），同一快照跨运行排序确定性提升。不做 `SkipSemanticRerank` 评估旁路——评估应测产品真实行为。
- `rerankAvailable`（Cohere 装配标志）不变；`rerankStats.poolSize / bestScore` 重排前采集，校准数据不受影响。
- **契约测试**：proto 新增 `rerank_model` / `judge_model` → generated DTO 变化 → `api/http/contract_test.go` golden **additive 更新**（与 v1"golden 无需改"不同；实现时确认零值是否序列化为 `""`）。

## 8. 测试

1. **domain `Validate`**：`reranking=builtin-score-v1` + 空 `rerank_model` → `ErrRerankModelRequired`；外部 rerank / 空 `reranking` 不要求模型；`embedding_model` 检查优先级不变。
2. **application 保存校验**：builtin + 有效 chat 模型 → 通过；模型不在目录 → `ErrInvalidRerankModel`；目录查询失败 → 拒绝（fail-closed）；`judge_model` 非空无效 → `ErrInvalidJudgeModel`；`judge_model` 空 → 通过（关闭门）。
3. **`rag_service`**：
   - `resolveWorkspaceConfig` 填充 `RerankModel` / `JudgeModel`；
   - `rerankSemantic` 传 `req.RerankModel`（非空哨兵）到 `RerankRequest.Model`；
   - judge resolver：workspace 配模型 → 判定生效；空模型 → 放行；resolver 失败 → WARN + 放行（fail-closed）；
   - 既有 fail-open / 降级 / topN 与池取 min / 补尾用例保持。
4. **wiring**：gateway 可用即注入 reranker（不再校验模型）；resolver 动态构造 judge（model 从参数）；gateway nil 不注入。
5. **config**：删除 6 个 env 后 `Load()` 测试更新（`AgentFactCheckConfig` 保留用例）。
6. **契约测试**：golden additive 更新，字段值契约不变。
7. **回归**：`go vet && go test -short ./...`；代码质量门禁（新函数复杂度 / 行数）。

## 9. 风险与开放点

- **阈值跨分数空间**（同 v1）：LLM 覆盖 Score 后 `ScoreThreshold` 过滤基于 LLM 分数（0-1）；fail-open 降级时回到召回分数空间。与 Cohere 外部重排先例一致，是启用/降级重排的固有语义。`BestScore / CandidateCount` 校准不受影响。
- **存量 workspace 保存拒绝**：§6 迁移 SQL 需覆盖所有 tenant（含历史租户），tenant_schema 与编号迁移的差异（JSONB 无需 DDL）已规避——`rerank_model` / `judge_model` 是 JSONB 内字段，**无新增列，不涉及 tenant DDL / 编号迁移**。
- **judge resolver 每查询构造**：无状态对象，代价可忽略；若未来模型目录校验需在运行期重复，再引入缓存（当前不 YAGNI）。
- **降级日志级别**：配置但失败打 WARN（非 ERROR）——降级期间多工作区扇出下避免 ERROR 洪水；告警由 degraded 指标率驱动。与 v1 裁决一致。
- **token 成本**：单次查询 ≤ `RerankLLMTopN × RerankLLMMaxDocRunes` ≈ 10 × 500 runes 输入 + 输出，量级可控。
- **指标**：标签固定 `builtin-llm`，不暴露模型名（模型现为 workspace 级，更不落日志）。
- **模型治理一致性**：embedding_model 走 `ModelExists(CapEmbedding)`，本版 rerank/judge 走 `ModelExists(CapChat)`——能力区分由目录 capability 决定，不做跨能力自动降级。

## 10. 验收标准

1. 保存 workspace：`reranking=builtin-score-v1` 无 `rerank_model` → 返回 `ErrRerankModelRequired`（前端展示引导）。
2. 配置 `rerank_model`（chat 目录内）后，builtin 查询对 top-N 候选产生语义排序，分数为 LLM 分数。
3. LLM 调用失败 / 超时 → WARN + degraded 指标 + 召回分数排序，检索不失败。
4. `judge_model` 配置后 evidence 路径：证据不足 → `NoAnswer=insufficient_evidence`；未配 / 失败 → 放行。
5. 全部 knowledge 域模型 env（6 个）从 `config/config.go` 删除，`AgentFactCheckConfig` 保留。
6. 最终返回条数 ≤ TopK；召回池不足 TopK 时全部返回。
7. `go test -short ./...` 全绿，代码质量门禁通过，契约测试 golden 已 additive 更新。

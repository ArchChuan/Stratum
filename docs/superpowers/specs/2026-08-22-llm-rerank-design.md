# 内置重排改造为 LLM 语义重排设计

日期：2026-08-22
状态：待实现（设计已获批准）
范围：`internal/knowledge/application/rag_service.go`、`internal/knowledge/infrastructure/rerank/llm.go`（新增）、`internal/knowledge/domain/port/reranker.go`（复用）、`api/wiring/knowledge.go`、`config/config.go`、`pkg/constants/knowledge.go`、`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`。

## 1. 背景与问题

RAG 检索流水线为：召回（vector / hybrid / keyword）→ 重排（rerank）→ 阈值过滤 → topK 截断。

内置重排策略 `builtin-score-v1` 当前实现（`rag_service.go` `rerankSources`）只是对候选池按 `Score` 降序做一次稳定排序。但所有到达 `rerankSources` 的池子已经按分数降序（vector 腿 Milvus 按距离升序、hybrid 腿 `rrfFuse` 已按融合分数降序），因此 **builtin 分支是确定性 no-op**：排序不改变任何结果顺序，不产生语义增量。

设计标准：**重排必须基于语义理解产生语义增量**。基于该标准与以下事实决策：

- 不引入 cross-encoder（用户决策："先不引入 cross-encoder，把内置重排用 LLM 网关解决，支持配置重排模型"）。
- 复用平台已有的 LLM 网关能力（`LLMGateway.Gateway` / `LLMCompleter.Complete`），与 `knowledgeJudge`（证据充分性 judge）同一先例。
- 数据外发走平台已有 chat provider（Qwen / Zhipu），信任边界与普通 chat 一致，无新第三方。

### 已确认的决策

1. **配置层级**：平台级配置（env 前缀 `KNOWLEDGE_RERANK_*`），全平台统一，仿 `KnowledgeJudgeConfig`。
2. **失败语义**：fail-open —— LLM 重排未配置 / 调用失败 / 超时 / 解析失败 → WARN + 指标（degraded）→ 降级为按召回分数排序（等价当前 builtin 行为）。检索永不因重排失败。
3. **重排范围**：仅对 top-N 精排 —— 先按召回分数取前 N 条（N 与池大小取 min），再做一次 listwise 打分。

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

- `rerankSources`（`rag_service.go:995-1019`）：重排分派核心。
- `rerankExternal`（`rag_service.go:1052-1086`）：Cohere 外部重排先例 —— `s.Score = r.Score` 覆盖分数、池收敛为返回结果、`MinRerankCandidates`（池 < 3 跳过）、`RerankMaxCandidates`（≤ 50）。
- `rerankStats{poolSize, bestScore, thresholdFilter}`（入口采集，重排前）——校准数据不受重排影响。
- `knowledgeJudge`（`api/wiring/knowledge_judge.go`）：LLM 判别先例 —— `context.WithTimeout`、`ResponseFormat json_object`、`MaxTokens`、`truncateRunes`。
- `LLMCompleter`（`internal/llmgateway/domain/llm.go`）：`Complete(ctx, *CompletionRequest) (*CompletionResponse, error)`。

## 3. 目标与非目标

### 目标

1. `builtin-score-v1` 产生语义增量：基于 LLM 语义对候选重新排序。
2. 平台级配置重排模型，全平台统一生效。
3. fail-open：重排不可用时检索行为与当前完全一致（按召回分数排序）。
4. 阈值过滤 / topK 截断 / 校准统计链路不变。
5. 不引入新第三方依赖，不修改 proto 契约。

### 非目标

- 不引入 cross-encoder。
- 不改变外部重排（`cohere:xxx`）行为。
- 不做多模型 listwise 组合 / LLM 交叉对比排序。
- 不新增 HTTP API / DTO 字段。

## 4. 设计

### 4.1 配置（平台级）

`config/config.go` 新增，仿 `KnowledgeJudgeConfig`：

```go
type KnowledgeRerankConfig struct {
    Model   string        // 重排 chat 模型；空 = 未启用（builtin 降级为纯排序）
    Timeout time.Duration // 单次 LLM 调用预算；0 用默认 RerankLLMTimeout
    TopN    int           // 先按召回分数取前 N 条精排；0 用默认 RerankLLMTopN
}
```

- `Config` 新增字段 `KnowledgeRerank KnowledgeRerankConfig`。
- env：`KNOWLEDGE_RERANK_MODEL` / `KNOWLEDGE_RERANK_TIMEOUT_SECONDS` / `KNOWLEDGE_RERANK_TOPN`。
- 新增 `func (c *Config) RerankLLMConfigured() bool { return c.KnowledgeRerank.Model != "" }`。
- 模型未配置时由 llmgateway 从模型目录解析；不写死兜底模型，与 `KnowledgeJudgeConfig.Model` 注释一致。

### 4.2 重排器（`internal/knowledge/infrastructure/rerank/llm.go`，新增）

**复用 `knowledgeport.Reranker` 接口**（`reranker.go`：`Rerank(ctx, RerankRequest{Query, Documents, Model, TopN}) ([]RerankResult{Index, Score}, error)`），与 Cohere 同形，无新接口。

```go
type LLMReranker struct {
    completer llmgatewaydomain.LLMCompleter // gateway 结构性满足
    model     string                        // 平台配置重排模型
    timeout   time.Duration                 // 单次调用预算
    metrics   observability.MetricsProvider // 可为 nil
    logger    *zap.Logger
}
```

`topN` 不存结构：重排器从 `RerankRequest.TopN` 读取（接口已传），与 Cohere 一致。构造函数 `NewLLMReranker(completer, model, timeout, metrics, logger)`。

`Rerank(ctx, req)` 流程（照抄 `knowledgeJudge` 先例）：

1. 构造 **listwise** prompt：
   - 系统："你是严谨的检索相关性评分法官。只输出 JSON，不输出其他内容。"
   - 用户：列出编号候选（每候选经 `truncateRunes(doc, RerankLLMMaxDocRunes)` 截断）+ 查询，要求输出 `{"scores":[{"index":i,"score":0..1},...]}`。
2. `ResponseFormat: &llmgatewaydomain.ResponseFormat{Type: "json_object"}`、`MaxTokens: RerankLLMMaxTokens`。
3. `ctx, cancel := context.WithTimeout(ctx, r.timeout)`，`defer cancel()`。
4. 调用 `r.completer.Complete`。
5. 解析 scores：按 `index` 映射回候选；缺失 index 用原序补尾，保证 top-N 完整。
6. 失败 / 超时 / 解析失败 → 返回 error（调用方降级），不 panic。

### 4.3 `RAGService` 改造

`RAGService` 新增字段 `semanticReranker knowledgeport.Reranker` 与 `semanticTopN int`（精排候选上限），注入点：

```go
func (rs *RAGService) SetSemanticReranker(r knowledgeport.Reranker, topN int)
// wiring 在注入前解析默认：topN <= 0 时用 constants.RerankLLMTopN
```

`rerankSources`（`rag_service.go:999`）`builtin-score-v1` 分支改造：

```go
case "builtin-score-v1":
    if rs.semanticReranker != nil && len(pool) >= 2 {
        narrowed, err := rs.rerankSemantic(ctx, req, pool)
        if err != nil {
            // fail-open：不返回 error，降级为召回分数排序；degraded 指标用
            // 固定标签（不暴露平台重排模型名）。
            rs.logger.Warn("knowledge.retrieval.llm_rerank_degraded", zap.Error(err))
            if rs.metrics != nil {
                rs.metrics.IncRerankRequest(req.TenantID, "builtin-llm", "degraded")
            }
        } else {
            pool = narrowed
        }
    }
    sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
```

新增 `rerankSemantic`：

```go
func (rs *RAGService) rerankSemantic(ctx context.Context, req RAGQueryRequest, pool []Source) ([]Source, error) {
    topN := min(rs.semanticTopN, len(pool))       // 精排候选数与池取 min
    docs := make([]string, topN)                  // 前 topN 条 Content
    for i := range docs { docs[i] = pool[i].Content }
    // Model 字段对 LLM 重排器是忽略项（重排器内部用平台配置模型），与 Cohere
    // 请求同构复用同一 Reranker 接口。
    results, err := rs.semanticReranker.Rerank(ctx, knowledgeport.RerankRequest{
        Query: req.Question, Documents: docs, Model: req.Reranking, TopN: topN,
    })
    if err != nil { return nil, err }
    narrowed := make([]Source, 0, topN)
    for _, r := range results {
        if r.Index >= 0 && r.Index < topN {
            s := pool[r.Index]
            s.Score = r.Score                       // LLM 分数覆盖召回分数（与 rerankExternal 先例一致）
            narrowed = append(narrowed, s)
        }
    }
    sort.SliceStable(narrowed, score desc)
    return narrowed, nil
}
```

关键语义：

- **topN 与池取 min**：池子 ≤ N 全量精排；池子 > N 只精排召回分数前 N，第 N+1 名以后不参与最终结果（"仅对 top-N 精排"的边界）。
- **覆盖 Score**：与 `rerankExternal`（`s.Score = r.Score` + 池收敛）先例一致，池内分数单一来源（LLM 分数），阈值过滤基于 LLM 分数。
- **池收敛为 topN 条**：重排后池子即精排候选，阈值过滤 / 截断在其上进行。
- `len(pool) >= 2` 才调用：单候选重排无意义（避免白付 LLM 延迟）。

### 4.4 Wiring（`api/wiring/knowledge.go`）

仿 `wireKnowledgeJudge` 新增 `wireSemanticReranker`，在 `buildKnowledge` 中调用：

```go
func (c *Container) wireSemanticReranker(rag *knowledge.RAGService) {
    if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.RerankLLMConfigured() {
        return
    }
    krc := c.Config.KnowledgeRerank
    topN := krc.TopN
    if topN <= 0 { topN = constants.RerankLLMTopN }
    timeout := krc.Timeout
    if timeout <= 0 { timeout = constants.RerankLLMTimeout }
    rag.SetSemanticReranker(rerank.NewLLMReranker(
        c.LLMGateway.Gateway, krc.Model, timeout, c.Platform.Metrics, c.Logger), topN)
}
```

`Platform.Metrics` 可能为 nil（`c.Platform` 判断），由 LLMReranker 容忍 nil metrics。

### 4.5 常量（`pkg/constants/knowledge.go`）

| 常量 | 值 | 语义 |
|---|---|---|
| `RerankLLMTopN` | 10 | 精排候选上限（默认） |
| `RerankLLMMaxTokens` | 1024 | LLM 输出预算 |
| `RerankLLMTimeout` | 15s | 单次调用预算 |
| `RerankLLMMaxDocRunes` | 500 | 单候选正文截断 |

### 4.6 前端（`WorkspaceConfigForm.tsx`）

`builtin-score-v1` tooltip 更新："内置重排由平台 LLM 模型语义精排；未配置时自动降级为分数排序"。`AllowedRerankIdentities` / `SplitRerankIdentity` / `ValidRerankIdentity` 不变。

### 4.7 数据流（最终形态）

```
vector/hybrid 召回 → pool（召回分数降序）
  → [builtin 分支] 取前 min(TopN, len(pool)) → LLM listwise 打分 → 覆盖 Score → 池收敛
  → 降级路径：保持召回分数排序
  → ScoreThreshold 过滤（基于 LLM 分数）
  → topK 截断（超才截）
  → 主链路 ≤ TopK 条
```

## 5. 错误处理与降级

- **未配置模型**：`semanticReranker == nil` → 保持现行为（排序），零调用。
- **调用失败 / 超时 / 解析失败**：`rerankSemantic` 返回 error → WARN 日志 + `IncRerankRequest(..., "degraded")` 指标 → 池不变 → 按召回分数排序。检索不失败。
- **`len(pool) < 2`**：跳过 LLM，直接排序。
- **LLM 返回缺 index / 非法 index**：跳过该结果；返回不足 topN 时按原序补齐不足部分（由调用方保证池完整）。

## 6. 评估影响

- evaluation 默认 `RerankingNone`（`retrieval_evaluator.go`），不受影响。
- 显式 `builtin-score-v1` 快照会触发 LLM 重排；失败自动降级，不阻塞评估（fail-open）。
- `rerankAvailable`（Cohere 装配标志）不变。
- `rerankStats.poolSize / bestScore` 在重排前采集，校准数据（`RAGQueryResult.BestScore / CandidateCount`、evaluation 校准）不受重排影响。

## 7. 测试

1. **`llm.go` 单测**：prompt 构造；JSON 解析（正常 / 缺 index / 非法 index / 坏 JSON / 超量 scores）；超时传播（fake completer 阻塞 → context deadline）；nil metrics 容忍。
2. **`rag_service` 单测**：
   - 注入 semanticReranker → builtin 走 LLM，Score 被覆盖，池收敛；
   - `semanticReranker == nil` → 降级排序（现行为）；
   - LLM 失败 → degraded 指标 + 降级排序，不返回 error；
   - `len(pool) < 2` → 跳过 LLM；
   - topN 与池取 min（池 3 条 → 精排 3 条）。
3. **wiring 测试**：Model 空不注入 / 非空注入 / gateway nil 不注入。
4. **契约测试**：字段值不变，`api/http/contract_test.go` golden 无需改。
5. **回归**：`go vet && go test -short ./...`；代码质量门禁（新函数复杂度 / 行数）。

## 8. 风险与开放点

- **阈值语义变化**：LLM 覆盖 Score 后，`ScoreThreshold` 过滤基于 LLM 分数（0-1）而非召回分数（vector 相似度 / hybrid RRF ~0.016）。与 Cohere 外部重排先例一致；对 hybrid 模式阈值终于生效，但用户已配阈值在切换重排后行为变化。`BestScore / CandidateCount` 校准不受影响。
- **LLM 打分稳定性**：listwise 排序质量依赖模型，靠 `TopN` 上限 + 降级兜底。若模型输出质量差，结果等价于原始排序（fail-open 不恶化）。
- **token 成本**：单次查询 ≤ `TopN × RerankLLMMaxDocRunes` ≈ 10 × 500 runes 输入 + 输出，量级可控。
- **指标**：`IncRerankRequest` 的 model 参数为平台重排模型名，不涉敏感信息。

## 9. 验收标准

1. 配置 `KNOWLEDGE_RERANK_MODEL` 后，`builtin-score-v1` 查询对 top-N 候选产生语义排序，分数为 LLM 分数。
2. 未配置 / LLM 失败 / 超时时，`builtin-score-v1` 行为与现状完全一致（召回分数排序），日志 WARN + degraded 指标。
3. 最终返回条数 ≤ TopK；召回池不足 TopK 时全部返回。
4. `go test -short ./...` 全绿，代码质量门禁通过。
5. 契约测试 golden 不变。

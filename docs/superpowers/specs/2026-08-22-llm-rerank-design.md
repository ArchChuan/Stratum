# 内置重排改造为 LLM 语义重排设计

日期：2026-08-22
状态：待实现（设计已获批准）
范围：`internal/knowledge/application/rag_service.go`、`api/wiring/llm_reranker.go`（新增，仿 `knowledge_judge.go` 先例）、`internal/knowledge/domain/port/reranker.go`（复用）、`api/wiring/knowledge.go`、`config/config.go`、`pkg/constants/knowledge.go`、`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`。

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

### 4.2 重排器（`api/wiring/llm_reranker.go`，新增）

**复用 `knowledgeport.Reranker` 接口**（`reranker.go`：`Rerank(ctx, RerankRequest{Query, Documents, Model, TopN}) ([]RerankResult{Index, Score}, error)`），与 Cohere 同形，无新接口。

**位置决策（review M3）**：LLM 消费适配器放组合根 `api/wiring/`，与 `knowledgeJudge` 同一先例。`architecture_test.go` 注释把"knowledge 零 LLM 依赖约束：judge 接口在 domain/port 消费，实现只在组合根"定为 knowledge 既定约束；`knowledge/infrastructure/rerank` 保持兄弟 context 零依赖（`cohere.go` 只依赖自身 context + `pkg/`），不引入首个跨 context 依赖。

```go
type llmReranker struct {
    completer llmgatewaydomain.LLMCompleter // Gateway 结构性满足
    model     string                        // 平台配置重排模型
    timeout   time.Duration                 // 单次调用预算
    metrics   observability.MetricsProvider // 可为 nil
    logger    *zap.Logger
}
```

`topN` 不存结构：重排器从 `RerankRequest.TopN` 读取（接口已传），与 Cohere 一致。构造函数 `newLLMReranker(completer, model, timeout, metrics, logger)`。

`Rerank(ctx, req)` 流程（照抄 `knowledgeJudge` 先例）：

1. 对 `req.Documents` 逐条 `truncateRunes(doc, RerankLLMMaxDocRunes)` 截断（wiring 包已有 `truncateRunes`；截断是 LLM 输入预算，属重排器实现细节，调用方传完整候选）。
2. 构造 **listwise** prompt：
   - 系统："你是严谨的检索相关性评分法官。只输出 JSON，不输出其他内容。"
   - 用户：列出编号候选（正文已截断）+ 查询，要求输出 `{"scores":[{"index":i,"score":0..1},...]}`。
3. `ResponseFormat: &llmgatewaydomain.ResponseFormat{Type: "json_object"}`、`MaxTokens: RerankLLMMaxTokens`、**`Temperature` 显式设 0**（确定性采样，评估快照回放可复现；review M4/F2）。
4. `ctx, cancel := context.WithTimeout(ctx, r.timeout)`，`defer cancel()`。
5. 调用 `r.completer.Complete`；镜像 Cohere `record()`：成功记 `IncRerankRequest(reqctx.TenantIDFromContext(ctx), "builtin-llm", "ok")` + `RecordRerankDuration`；调用失败记 `"error"` 后返回 error（`metrics` 可为 nil，跳过记录）。tenant 从 `reqctx` 取——`RerankRequest` 无 tenant 字段，与 Cohere 一致（review L2）。
6. 解析 scores：按 `index` 映射回候选返回；**对重复 index 去重**（LLM JSON 输出不保证唯一，保留首次出现）。**结果不足 topN 时的补尾由调用方 `rerankSemantic` 负责**（§4.3），本层只返回 LLM 实际给出的结果。
7. 失败 / 超时 / 解析失败 → 返回 error（调用方降级），不 panic。

**指标标签统一为固定 `builtin-llm`**（不暴露平台重排模型名），三态：`ok`（LLM 重排成功，Rerank 内记）、`error`（LLM 调用失败，Rerank 内记）、`degraded`（检索降级为分数排序，调用方记），可算 degraded 率。

> **截断职责**：`truncateRunes` 仅存在于 `internal/knowledge/application`、`api/wiring`、`internal/agent/application`（均未导出）。重排器位于 `api/wiring`，在内部用 wiring 的 `truncateRunes` 完成候选截断，调用方传完整候选；补尾去重由本层与调用方双保险。

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
            // 固定标签（不暴露平台重排模型名）。tenant 从 ctx 取号（RerankRequest
            // 无 tenant 字段，与 Cohere 一致）。日志 WARN 且不含 query/chunk/
            // response 原文（红线）；告警由 degraded 指标率驱动，避免降级期间
            // 每查询 ERROR 洪水（review F5 的 ERROR 建议不采纳，理由见 §8）。
            rs.logger.Warn("knowledge.retrieval.llm_rerank_degraded",
                zap.Error(err), zap.Int("pool_size", len(pool)))
            if rs.metrics != nil {
                rs.metrics.IncRerankRequest(reqctx.TenantIDFromContext(ctx), "builtin-llm", "degraded")
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
    topN := min(rs.semanticTopN, len(pool)) // 精排候选数与池取 min
    if topN < 2 {
        return pool, nil // 单候选/空语义重排无意义，fail-open 保持池走排序
    }
    docs := make([]string, topN) // 前 topN 条 Content；截断由重排器内部负责（§4.2）
    for i := range docs {
        docs[i] = pool[i].Content
    }
    // Model 传空哨兵：LLM 重排器内部用平台配置模型，忽略该字段；传
    // req.Reranking（builtin-score-v1[:model]）会误导为请求级模型（review L3）。
    results, err := rs.semanticReranker.Rerank(ctx, knowledgeport.RerankRequest{
        Query: req.Question, Documents: docs, Model: "", TopN: topN,
    })
    if err != nil {
        return nil, err
    }
    narrowed := make([]Source, 0, topN)
    used := make(map[int]struct{}, len(results))
    for _, r := range results {
        if r.Index < 0 || r.Index >= topN {
            continue
        }
        if _, ok := used[r.Index]; ok {
            continue // 重排器已去重，此处防御性保留
        }
        used[r.Index] = struct{}{}
        s := pool[r.Index]
        s.Score = r.Score // LLM 分数覆盖召回分数（与 rerankExternal 先例一致）
        narrowed = append(narrowed, s)
    }
    // 补尾：LLM 返回不足 topN 时按原序补入未被打分的候选，Score 沿用原召回
    // 分数（不覆盖）——仅发生在 LLM 部分返回的异常路径，其候选在 LLM 分数
    // 空间阈值下可能被过滤，属预期语义（review M2）。
    for i := 0; i < topN && len(narrowed) < topN; i++ {
        if _, ok := used[i]; !ok {
            narrowed = append(narrowed, pool[i])
        }
    }
    // 不做内部排序：收敛后的池统一由 rerankSources 的 sort.SliceStable 排序，
    // 与降级路径同构，避免双重排序（review L1）。
    return narrowed, nil
}
```

关键语义：

- **topN 与池取 min**：池子 ≤ N 全量精排；池子 > N 只精排召回分数前 N，第 N+1 名以后不参与最终结果（"仅对 top-N 精排"的边界）。
- **topN < 2 守卫**：`semanticTopN <= 1` 或池 < 2 时保持池不变走排序（fail-open，防止 `topN=0` 覆盖空池破坏检索，review M1）。与外部重排 `MinRerankCandidates=3` 的差异是有意的：LLM 一次 listwise 调用成本低于外部，2 条即起排。
- **覆盖 Score**：与 `rerankExternal`（`s.Score = r.Score` + 池收敛）先例一致，池内分数单一来源（LLM 分数），阈值过滤基于 LLM 分数；补尾候选沿用原召回分数（本段补尾循环）。
- **池收敛为 topN 条**：重排后池子即精排候选，阈值过滤 / 截断在其上进行。`semanticTopN` 因此是最终条数的硬上限（配置须 ≥ 常用 TopK，默认 10 > 5，review M5）。
- `len(pool) >= 2` 才调用：单候选重排无意义（避免白付 LLM 延迟）。

### 4.4 Wiring（`api/wiring/knowledge.go`）

仿 `wireKnowledgeJudge` 新增 `wireSemanticReranker`，在 `buildKnowledge` 中调用（`buildKnowledge(ctx)` 有 ctx，传入目录校验）：

```go
func (c *Container) wireSemanticReranker(ctx context.Context, rag *knowledge.RAGService) {
    if r, topN := c.semanticRerankerDeps(ctx); r != nil {
        rag.SetSemanticReranker(r, topN)
    }
}

// semanticRerankerDeps 解析并构建 LLM 语义重排器；任一前置条件不满足返回
// (nil, 0)。topN/timeout 的 ≤0 默认值在此解析（回落常量），使 wiring 层
// 单测可直接验证注入决策而无需构造完整 Gateway（RAGService.semanticReranker
// 为 application 包未导出字段，行为探针会因 Gateway.Complete nil-panic）。
func (c *Container) semanticRerankerDeps(ctx context.Context) (knowledgeport.Reranker, int) {
    if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.RerankLLMConfigured() {
        return nil, 0
    }
    krc := c.Config.KnowledgeRerank
    if !c.llmRerankModelInCatalogue(ctx, krc.Model) {
        // fail-open 装配：模型不在 chat 目录 → WARN + 不注入，builtin 走纯排序。
        // 配置错误在启动期暴露，而非运行期每查询失败（review F7）。
        c.Logger.Warn("knowledge.rerank.model_unavailable",
            zap.String("model", krc.Model), zap.String("reason", "model not in chat catalogue"))
        return nil, 0
    }
    topN := krc.TopN
    if topN <= 0 {
        topN = constants.RerankLLMTopN
    }
    timeout := krc.Timeout
    if timeout <= 0 {
        timeout = constants.RerankLLMTimeout
    }
    var metrics observability.MetricsProvider
    if c.Platform != nil {
        metrics = c.Platform.Metrics // c.Platform 可能为 nil（review H2）
    }
    return newLLMReranker(c.LLMGateway.Gateway, krc.Model, timeout, metrics, c.Logger), topN
}

// llmRerankModelInCatalogue 检查平台配置的重排模型是否在 chat 模型目录。
// 目录查询失败按"不在目录"处理（fail-open 不注入），不静默放行。
func (c *Container) llmRerankModelInCatalogue(ctx context.Context, model string) bool {
    if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
        return false
    }
    names, err := c.LLMGateway.Registry.ListChatModelsByTenant(ctx)
    if err != nil {
        c.Logger.Warn("knowledge.rerank.catalogue_unavailable", zap.Error(err))
        return false
    }
    for _, n := range names {
        if n == model {
            return true
        }
    }
    return false
}
```

`Platform.Metrics` 可能为 nil（`c.Platform` 判断），由 `llmReranker` 容忍 nil metrics。

### 4.5 常量（`pkg/constants/knowledge.go`）

| 常量 | 值 | 语义 |
|---|---|---|
| `RerankLLMTopN` | 10 | 精排候选上限（默认），须 ≥ 常用 TopK（默认 5）；配置 < 工作区 TopK 时，最终条数受 `semanticTopN` 硬上限约束（review M5） |
| `RerankLLMMaxTokens` | 1024 | LLM 输出预算 |
| `RerankLLMTimeout` | 5s | 单次调用预算（review F4：15s 尾部延迟过大，下调至 5s） |
| `RerankLLMMaxDocRunes` | 500 | 单候选正文截断 |

### 4.6 前端（`WorkspaceConfigForm.tsx`）

`builtin-score-v1` tooltip 更新："内置重排由平台 LLM 模型语义精排，需在模型管理中配置重排模型；未配置时自动降级为分数排序。外部重排需在模型管理中配置"（保留原"外部重排需在模型管理中配置"指引，review L3）。`AllowedRerankIdentities` / `SplitRerankIdentity` / `ValidRerankIdentity` 不变。

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

- **未配置模型 / 模型不在目录**：`semanticReranker == nil` → 保持现行为（排序），零调用。
- **调用失败 / 超时 / 解析失败**：`rerankSemantic` 返回 error → WARN 日志 + `IncRerankRequest(..., "degraded")` 指标 → 池不变 → 按召回分数排序。检索不失败。
- **`len(pool) < 2` / `topN < 2`**：跳过 LLM，直接排序。
- **LLM 返回缺 index / 非法 index / 重复 index**：跳过非法、去重重复；返回不足 topN 时按原序补入未被打分的候选，**Score 沿用原召回分数**（不覆盖）——补尾仅在异常路径发生，其候选在 LLM 分数空间阈值下可能被过滤（review M2）。
- **日志红线（review F5）**：降级 / 失败日志只记录 model / topN / pool_size / error 摘要，**禁止记录 query / chunk / response 原文**；重排失败不落任何检索正文。

## 6. 评估影响

- evaluation 默认 `RerankingNone`（`retrieval_evaluator.go`），不受影响。
- 显式 `builtin-score-v1` 快照会触发 LLM 重排；失败自动降级，不阻塞评估（fail-open）。
- **可复现性（review M4/F2）**：`Temperature=0` 固定采样，同一快照跨运行排序确定性大幅提升。不做 `SkipSemanticRerank` 评估旁路——评估应测产品真实行为（builtin 语义已从 score-desc 变更为 LLM 语义），温度 0 已收敛抖动；若未来评估对比出现不稳定，再加旁路。
- `rerankAvailable`（Cohere 装配标志）不变。
- `rerankStats.poolSize / bestScore` 在重排前采集，校准数据（`RAGQueryResult.BestScore / CandidateCount`、evaluation 校准）不受重排影响。

## 7. 测试

1. **`llm_reranker` 单测**（wiring 包）：prompt 构造；候选截断；JSON 解析（正常 / 缺 index / 非法 index / 重复 index / 坏 JSON / 超量 scores）；超时传播（fake completer 阻塞 → context deadline）；nil metrics 容忍；`Temperature=0` 断言（fake completer 捕获请求）。
2. **`rag_service` 单测**：
   - 注入 semanticReranker → builtin 走 LLM，Score 被覆盖，池收敛；
   - `semanticReranker == nil` → 降级排序（现行为）；
   - LLM 失败 → degraded 指标 + 降级排序，不返回 error；
   - `len(pool) < 2` / `topN=0/1` → 跳过 LLM，池不变（review M1）；
   - topN 与池取 min（池 3 条 → 精排 3 条；池 20 条 → 精排 topN 条，第 topN+1 名不返回）；
   - LLM 部分返回 index → 补尾沿用原召回 Score + 阈值过滤在 LLM 分数空间执行的差异断言（review M2/L2）。
3. **wiring 测试**：Model 空不注入 / 非空且在目录注入 / 非空不在目录不注入（WARN）/ gateway nil 不注入 / `c.Platform == nil` 不 panic（review H2/F7）。
4. **config 测试**：`KNOWLEDGE_RERANK_MODEL` / `_TIMEOUT_SECONDS` / `_TOPN` 环境变量解析与默认回落（review L4）。
5. **契约测试**：字段值不变，`api/http/contract_test.go` golden 无需改。
6. **回归**：`go vet && go test -short ./...`；代码质量门禁（新函数复杂度 / 行数）。

## 8. 风险与开放点

- **阈值跨分数空间（review F3）**：LLM 覆盖 Score 后，`ScoreThreshold` 过滤基于 LLM 分数（0-1）而非召回分数（vector 相似度 / hybrid RRF ~0.016）；fail-open 降级时同一阈值又回到召回分数空间。这是启用/降级重排的固有语义翻转，与 Cohere 外部重排先例一致：对 hybrid 模式阈值终于生效，但用户已配阈值在切换重排后行为变化。不引入 `score_space` 指标标签（避免过度设计；部署期阈值调试靠降级路径日志 + degraded 率）。`BestScore / CandidateCount` 校准不受影响。
- **降级日志级别（review F5 的分歧记录）**：配置但失败打 WARN（非 ERROR）——降级期间每查询失败会造成多工作区扇出下的 ERROR 洪水；告警由 `degraded` 指标率驱动（Prometheus rule）。若团队偏好日志告警，可升为 ERROR 并配 sampling/限流，属部署决策，不在代码内硬编码。
- **LLM 打分稳定性**：listwise 排序质量依赖模型，靠 `TopN` 上限 + 降级兜底。若模型输出质量差，结果等价于原始排序（fail-open 不恶化）。
- **token 成本**：单次查询 ≤ `TopN × RerankLLMMaxDocRunes` ≈ 10 × 500 runes 输入 + 输出，量级可控。
- **指标**：标签统一固定 `builtin-llm`，不暴露平台重排模型名，不涉敏感信息。
- **BestScore 跨空间（review F8）**：`BestScore` 语义继承自 Cohere 外部重排先例（LLM/外部分数与召回分数混用），非本次引入；evaluation 校准数据仍可比，但跨重排配置的横向对比需注意分数空间差异。
- **结果集差异（review F9）**：LLM 成功路径池收敛为 `semanticTopN` 条；降级路径保持召回池。`retrieved_count` 在两种路径下可能不同（配置 `semanticTopN < TopK` 时成功路径更少），属预期。
- **启动期模型校验（review F7）**：重排模型不在 chat 目录 → WARN + 不注入（fail-open 装配），配置错误在启动期暴露而非运行期每查询失败。

## 9. 验收标准

1. 配置 `KNOWLEDGE_RERANK_MODEL` 后，`builtin-score-v1` 查询对 top-N 候选产生语义排序，分数为 LLM 分数。
2. 未配置 / LLM 失败 / 超时时，`builtin-score-v1` 行为与现状完全一致（召回分数排序），日志 WARN + degraded 指标。
3. 最终返回条数 ≤ TopK；召回池不足 TopK 时全部返回。
4. `go test -short ./...` 全绿，代码质量门禁通过。
5. 契约测试 golden 不变。

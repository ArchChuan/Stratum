# 评测人工评审池（P1c）设计

日期：2026-08-30
范围：`internal/evaluation` 观测 + 评测集 judge 双覆盖
前置：P1a（观测管线）、P1b（判异信号 + `eval_observation_total` 指标）

## 1. 背景

P1b 已完成观测落库（`eval_observations`）、判异判定（rule/behavior/judge 三信号 → pass/flag/block）与观测指标。但自动判定存在两类信任缺口：

1. **judge 置信度不可见**：`applyJudge` 硬编码 `Confidence: 1.0`（observation_service.go:238 有 TODO(P1b)），低置信判定与高置信判定无法区分，也无从判断哪条判定值得人工复核。
2. **误判无处沉淀**：judge 判定错误、case 需修正、产品缺陷没有人工复核与回写通道，无法形成校准数据与评测集改进闭环。

本阶段（P1c）建设**人工评审池**（P1b spec §6.6）：满足触发条件的判异/低置信 case 上升评审池，人工 review 后回写 4 分类结论，落地「运行态沉淀 → 评测集」「judge 误判 → 校准样本」「产品缺陷 → 归因条目」的完整闭环，并建设 `eval_review_backlog` 积压指标 + 告警。

## 2. 目标与成功标准

目标：

- judge 契约输出真实置信度（confidence），低置信判定可识别。
- 观测判异与评测集 judge 判定中满足触发条件的条目进入评审池。
- 人工 review 后回写结论（pass/fail/judge_misjudgment/case_revision），并选择性沉淀/校准/归因。
- 评审池积压可观测（`eval_review_backlog` Gauge）并有告警。

成功标准（HowToTest）：

- 观测低置信（confidence < 阈值）触发入池；评测集 `NeedsReview=true` 或低置信触发入池。
- decision 幂等：已 `reviewed` 条目再次 decision 返回明确错误。
- 回写副作用：`judge_misjudgment` → 校准样本表；`fail` → 归因条目表；promote → 评测集 draft case。
- 前端评审池 Tab 可列表、查看详情、做判定。
- `eval_review_backlog` 随入池/处理增减；超阈值触发告警。

## 3. 设计决策

| # | 问题 | 决策 |
|---|------|------|
| D1 | 评审池范围 | **观测 + 评测集 judge 双覆盖** |
| D2 | judge 置信度 | **扩展 judge 契约输出真实 confidence**（缺失回退 1.0） |
| D3 | fail-closed 语义 | **纯监控台 + 积压告警**，不阻塞执行、不改 run 结论 |
| D4 | 回写动作 | **完整闭环（轻量记录）**：回写标记 + promote 沉淀 + 校准样本表 + 归因条目表；§9 归因分析引擎留后续阶段 |
| D5 | 评审池架构 | **方案 A：内联触发**（触发判定内联在落库/判定后，纯函数，无异步队列）+ 独立评审池模块 |

## 4. 架构

方案 A 内联触发：

- 观测路径：`ObservationService.Process` 落库后调用 `ReviewService.TryEscalateObservation` → 纯函数触发判定 → 入库。
- 评测集路径：`Service.runCase` 中 `judgeCase` 得出 `AssertionResult` 后调用 `ReviewService.TryEscalateCaseResult` → 入库。
- 评审池是独立 domain/application/infrastructure 模块：`domain/review_pool.go`、`application/review_service.go`、`infrastructure/persistence/review_repository.go`。
- 人工 review 走 HTTP API + 前端 Tab；无异步 worker，积压告警兜底。

评审池**不阻塞**观测落库与评测集判定（D3，监控语义单向上升）。`TryEscalate*` 失败 fail-open：主流程照常，仅日志 + `IncEvalReviewEscalateFailure` 指标。

## 5. 数据模型（tenant-scoped，入 `pkg/storage/postgres/tenant_schema.sql`）

### 5.1 `eval_review_items` — 评审池主表

```sql
CREATE TABLE IF NOT EXISTS eval_review_items (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL CHECK (source_type IN ('observation','case_result')),
    source_id      TEXT NOT NULL,
    run_id         TEXT NOT NULL DEFAULT '',
    trace_id       TEXT NOT NULL DEFAULT '',
    resource_kind  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    trigger_reason TEXT NOT NULL CHECK (trigger_reason IN ('low_confidence','dimension_split','judge_rule_conflict','needs_review')),
    snapshot       JSONB NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reviewed')),
    human_verdict  TEXT NOT NULL DEFAULT '' CHECK (human_verdict IN ('','pass','fail','judge_misjudgment','case_revision')),
    reviewer       TEXT NOT NULL DEFAULT '',
    review_reason  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_eval_review_items_dedup
    ON eval_review_items (source_type, source_id, trigger_reason);
CREATE INDEX IF NOT EXISTS idx_eval_review_items_status
    ON eval_review_items (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eval_review_items_resource
    ON eval_review_items (resource_kind, resource_id, created_at DESC);
```

说明：

- `source_id`：观测 = `eval_observations.id`；评测集 = `eval_case_results.id`。
- `snapshot`：观测 = 完整 `EvalObservation` JSON；评测集 = case 输入/期望/判定/信号快照。
- 去重：同 `(source_type, source_id, trigger_reason)` 只入池一次（幂等触发，重复调用不产生重复条目）。
- `status=reviewed` 后 `human_verdict` 必填。

### 5.2 `eval_calibration_samples` — judge 误判校准样本

```sql
CREATE TABLE IF NOT EXISTS eval_calibration_samples (
    id             TEXT PRIMARY KEY,
    review_item_id TEXT NOT NULL REFERENCES eval_review_items(id) ON DELETE RESTRICT,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    judge_model    TEXT NOT NULL DEFAULT '',
    signals        JSONB NOT NULL DEFAULT '{}',
    human_verdict  TEXT NOT NULL,
    reviewer       TEXT NOT NULL,
    reason         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_calibration_samples_source
    ON eval_calibration_samples (source_type, source_id);
```

说明：`judge_misjudgment` 判定时写入。`signals` 存该次 judge 判定快照（dimension/score/confidence/reason），供后续校准阶段消费。

### 5.3 `eval_attribution_entries` — 产品缺陷归因条目

```sql
CREATE TABLE IF NOT EXISTS eval_attribution_entries (
    id             TEXT PRIMARY KEY,
    review_item_id TEXT NOT NULL REFERENCES eval_review_items(id) ON DELETE RESTRICT,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    resource_kind  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    dimension      TEXT NOT NULL DEFAULT '',
    snapshot       JSONB NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    reviewer       TEXT NOT NULL,
    reason         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_attribution_entries_resource
    ON eval_attribution_entries (resource_kind, resource_id, created_at DESC);
```

说明：`fail` 判定时写入（观测来源含 trace/resource 锚点）。§9 归因分析引擎（三级根因、TunableEvalProfile、改进闭环）留后续阶段，本阶段只做轻量记录。

### 5.4 既有实体增强（additive，不破坏现有行为）

| 文件 | 实体 | 增强 |
|------|------|------|
| `internal/evaluation/domain/evaluation.go` | `AssertionResult` | `Confidence float64`（0-1，解析缺失默认 1.0） |
| `internal/evaluation/domain/observation.go` | `JudgeSignal` | `Reason string`（judge 打分理由） |
| `internal/evaluation/domain/evaluation.go` | `EvalCase` | `NeedsReview bool`（评测集显式标记需人工复核） |
| `internal/evaluation/domain/evaluation.go` | `EvalCaseResult` | `ID string`（在 `runCase` 生成稳定 ID，兼作 `eval_case_results.id` 与评审条目 `source_id`——原实现在 SaveRun 持久化时才生成，评测集触发早于落库，必须先有 ID） |
| `internal/evaluation/domain/port/evaluation.go` | `ObservedTrace` | `Tools []ToolObservation`（从 agent `TraceEvidence.Tools` 映射，供评审详情展示工具序列） |

## 6. judge 契约扩展

现状：`LLMJudge.Judge(ctx, JudgeRequest) → AssertionResult{Passed, Message}`；rubric 输出 `{"passed","reason"}`；`parseJudgeResponse`（api/wiring/evaluation.go:801）解析。

扩展：

- rubric 输出增加 `confidence`：`judgeRubric(dimension)` 与 `judgeDefaultRubric` 的 system prompt 要求输出 `{"passed": true/false, "reason": "一句话理由", "confidence": 0.0-1.0}`。
- `parseJudgeResponse` 解析 `confidence`：缺失/非法 → 默认 1.0（fail-open 回退，不丢判定）。
- 观测路径 `applyJudge` 用真实 confidence 填充 `JudgeSignal.Confidence`（消除硬编码 1.0 的 TODO(P1b)）；`JudgeSignal.Reason` 由 `reason` 填充。

## 7. 触发规则（domain 纯函数）

新文件 `internal/evaluation/domain/review_pool.go`：

```go
// ReviewTriggerReason 入池原因（§6.6 判定分歧/低置信/复杂 case）。
type ReviewTriggerReason string

const (
    TriggerLowConfidence     ReviewTriggerReason = "low_confidence"      // judge 低置信
    TriggerDimensionSplit    ReviewTriggerReason = "dimension_split"     // 维度判定分歧
    TriggerJudgeRuleConflict ReviewTriggerReason = "judge_rule_conflict" // judge pass 但规则护栏 block
    TriggerNeedsReview       ReviewTriggerReason = "needs_review"        // 评测集显式标记
)

// ReviewConfig 评审池触发配置（阈值来自平台参数，见 §10）。
type ReviewConfig struct {
    LowConfidenceThreshold float64 // 默认 0.6
}

// TriggersForObservation 返回观测应入池的触发原因（空 = 不进池）。
func TriggersForObservation(obs *EvalObservation, cfg ReviewConfig) []ReviewTriggerReason
// TriggersForCaseResult 返回评测集判定应入池的触发原因。
func TriggersForCaseResult(needsReview bool, assertion AssertionResult, cfg ReviewConfig) []ReviewTriggerReason
```

规则明细：

- **观测**：
  - `low_confidence`：存在维度 `JudgeSignal.Confidence < cfg.LowConfidenceThreshold`。
  - `dimension_split`：存在维度 `Score >= JudgeBelowThreshold` 且存在维度 `Score < JudgeBelowThreshold`（部分 pass 部分 fail）。
  - `judge_rule_conflict`：`len(Signals.Rule) > 0` 且 verdict 为 block，且全维度 judge pass（rule 与 judge 冲突）。
- **评测集**：
  - `low_confidence`：`AssertionResult.Confidence < cfg.LowConfidenceThreshold`。
  - `needs_review`：`EvalCase.NeedsReview == true`。

`JudgeBelowThreshold`：沿用 P1b judge pass 阈值，规划阶段锁定常量名。

## 8. 后端组件

### 8.1 `ReviewService`（`internal/evaluation/application/review_service.go`）

```go
type ReviewService struct {
    repo     port.ReviewRepository
    suites   port.SuiteRepository      // promote 沉淀 case（复用 AddDraftCases）
    evidence port.TraceEvidenceReader  // promote 观测条目时解析 trace 取 input/output
    metrics  observability.MetricsProvider
    logger   *zap.Logger
    cfg      func() ReviewConfig
}

// TryEscalateObservation 观测落库后内联触发（幂等）。
func (s *ReviewService) TryEscalateObservation(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
// TryEscalateCaseResult 评测集判定后内联触发（幂等）；runID 为当前 run（触发早于
// SaveRun，结果 ID 由 runCase 先生成）。
func (s *ReviewService) TryEscalateCaseResult(ctx context.Context, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) error
// List 分页列出评审池条目（可按 status/trigger_reason/资源过滤）。
func (s *ReviewService) List(ctx context.Context, tenantID string, f ReviewFilter) ([]domain.EvalReviewItem, int64, error)
// Get 取单条（含 snapshot 供前端渲染详情）。
func (s *ReviewService) Get(ctx context.Context, tenantID string, id string) (*domain.EvalReviewItem, error)
// Decision 人工判定回写（幂等：已 reviewed 返回 ErrReviewItemAlreadyReviewed）。
func (s *ReviewService) Decision(ctx context.Context, tenantID string, cmd ReviewDecisionCommand) error
// Backlog 当前 pending 数（供前端角标/告警采样）。
func (s *ReviewService) Backlog(ctx context.Context, tenantID string) (int64, error)
```

`Decision` 副作用（按 `HumanVerdict`）：

| verdict | 副作用 |
|---------|--------|
| `pass` | 仅回写标记（判定正确，人工确认） |
| `fail` | 写 `eval_attribution_entries`（产品缺陷归因） |
| `judge_misjudgment` | 写 `eval_calibration_samples`（judge 误判校准） |
| `case_revision` | 仅回写标记（case 修正走评测集 case 编辑流程，本阶段只记录） |
| `promote`（布尔） | 条目 → 生成 draft case 进目标 `suite_revision_id`（reviewer 指定），入评测集沉淀 |

`Decision` 状态机：仅 `pending` 可判定；已 `reviewed` → `ErrReviewItemAlreadyReviewed`（幂等拒绝）。`reviewer` 与 `review_reason` 必填。

### 8.2 `ReviewRepository`（`internal/evaluation/infrastructure/persistence/review_repository.go`）

- 全部方法经 `execTenant(ctx, tenantID, fn)`。
- port `domain/port/review.go`：`UpsertItem（幂等入池，ON CONFLICT DO NOTHING，返回是否插入） / GetItem / ListItems / MarkReviewed / CreateCalibrationSample / CreateAttributionEntry / CountPending`，签名显式含 `tenantID string`。
- promote 沉淀复用既有 `port.SuiteRepository.AddDraftCases`（spec 原列 `CreateDraftCase` 撤销，避免重复实现）。

### 8.3 指标

`pkg/observability/provider.go` 新增：

- `SetEvalReviewBacklog(value float64)`（Gauge `eval_review_backlog`，§11.2）
- `IncEvalReviewEscalateFailure()`（Counter，入池失败）

Prometheus impl 与 Noop stub 同步实现。

`eval_review_backlog` 为**全局 Gauge（无 tenant label）**，与 P1b `eval_queue_backlog` 同语义——`ReviewService.Backlog` 按租户计数，落指标时跨租户求和；积压告警面向运维整体视角，不做 per-tenant 告警（与现有 eval 告警模式一致）。

## 9. API 契约

HTTP 路由挂 `evaluations` group，与现有 `/observations` 并列；proto message 追加至 `proto/evaluation/evaluation.proto` → `make proto-gen`：

| Method | Path | 说明 |
|--------|------|------|
| GET | `/evaluations/review` | 分页列表（status/trigger_reason/资源过滤） |
| GET | `/evaluations/review/:id` | 单条详情（含 snapshot） |
| POST | `/evaluations/review/:id/decision` | 人工判定（verdict + reason + reviewer + promote 开关 + suite_revision_id） |

权限：`requireAdmin`（与 `/observations`、`/candidates` 同级管理操作）。契约黄金文件 `api/http/contract_test.go` + `testdata/contracts/*.golden.json` 同步更新。

## 10. 配置

阈值走平台参数系统（P1b `evaluation.*` 参数域），默认值落 `pkg/constants/evaluation.go`：

- `evaluation.review.low_confidence_threshold` = 0.6
- `evaluation.review.backlog_alert_threshold` = 50（积压告警阈值）
- judge pass 阈值沿用现有常量

禁止内联行为数字。

## 11. 前端操作台

`web/src/modules/evaluation/pages/EvaluationCenterPage.tsx` 新增 Tab「评审池」：

- 列表：pending/reviewed 切换，触发原因/资源过滤，pending 角标。
- 详情 Drawer：snapshot 渲染（观测：trace 链接 + 各维度 judge 信号 + 工具序列；评测集：case 输入/期望/判定 + judge 理由）。
- 判定操作：pass / fail / judge_misjudgment / case_revision 四分类 + 理由 + 沉淀开关；`Modal.confirm` 确认；`message.success/error` 反馈。
- 前端常量（分页、默认值）落 `web/src/constants/`。

## 12. 告警与飞书

`monitoring/remote/rules/stratum-evaluation.yaml` 新增 `StratumEvalReviewBacklogHigh`：

- expr：`eval_review_backlog > 50` 持续 15m（阈值来自 §10 配置）
- severity warning，annotations 含 runbook_url（评审池处置 runbook），复用 P1b Alertmanager → 飞书通道。

## 13. 测试策略

| 层 | 用例 | 覆盖 |
|----|------|------|
| 触发纯函数 | `domain/review_pool_test.go` 表驱动 | 低置信/维度分歧/rule 冲突/needs_review 各触发 + 边界（confidence=阈值、无 judge 信号、rule 空） |
| judge 契约 | `parseJudgeResponse` 解析测试 | confidence 正常/缺失/非法 → 回退 1.0；reason 传递 |
| ReviewService | unit（mock ReviewRepository + Metrics） | 幂等入池、decision 状态机、各 verdict 副作用、backlog 增减、escalate 失败 fail-open |
| ReviewRepository | integration（tenant db） | 三表 CRUD、去重索引、snapshot JSON 往返、execTenant 校验 |
| API 契约 | contract_test + golden | 三路由请求/响应契约 |
| 前端 | component test | Tab 列表/详情/判定交互 |
| 系统验收 | stratum-e2e-tester | `internal/evaluation/**` 命中外部依赖规则 → R3 → 600s soak |

## 14. 非目标

- §9 归因分析引擎（三级根因、TunableEvalProfile、改进闭环）→ 后续阶段。
- judge 自动重跑/自校准 → 后续阶段。
- 评审池阻塞执行（fail-closed 门禁语义）→ 本阶段明确不做。
- 观测自动入评测集（无人工确认自动沉淀）→ 需 reviewer 明确 promote。

## 15. Global Constraints（继承 CLAUDE.md）

- 禁止在 `main` 分支直接提交/推送；worktree 从最新 `origin/main` 创建。
- tenant-only DDL 只进 `pkg/storage/postgres/tenant_schema.sql`（`IF NOT EXISTS` 幂等、历史租户列 backfill），禁止进 `pkg/migration/sql/`。
- tenant-scoped repository 必须 `execTenant(ctx, tenantID, fn)`；port 方法签名显式含 `tenantID string`。
- DDD 分层：`domain` 仅 stdlib；`application` 不 import pgx/Redis/NATS/Gin；跨 context 接口定义在消费方 `domain/port/`。
- 参数契约事实源是 `proto/`；改契约 = 改 proto → `make proto-gen`（生成物不入 git）。
- 行为数字不内联：跨包 `pkg/constants/<domain>.go`；名称含 Default/Max/Min/单位语义。
- Go 代码质量：圈复杂度 ≤10、认知复杂度 ≤15、行数 ≤120、嵌套 ≤4；import 分组 stdlib/third-party/internal。
- 错误逐层 `fmt.Errorf("op: %w", err)` 包装；日志只用 Zap，禁 `fmt.Print`；禁记录 password/token/API key/PII。
- 前端：唯一 Axios 实例 `web/src/services/client.ts`；`message.error/success`、`Modal.confirm`；禁 alert/confirm/console.log；用户可见中文；常量入 `web/src/constants/`。
- 验证：`internal/evaluation/**` 命中外部依赖规则 → R3，由 stratum-e2e-tester 执行 600s soak；禁止绕过 skill 直接跑 make 替代系统验收。
- 远程写操作（kubectl/helm/镜像/DDL/DML）须先获用户许可；浏览器验证用无头 Playwright。

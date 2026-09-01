# §6.6 人工评审通道补齐 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development 逐任务实现本计划。Steps 用 `- [ ]` 跟踪。

**Goal:** 补齐 §6.6 人工评审通道两项细化：(1) 置信度机制边界——边界分数/理由含糊视为低置信；(2) 评审池按风险排序——安全/写操作/高危资源优先。

**Architecture:** 置信度边界是 domain 纯函数（`isBoundaryConfidence`/`hasVagueReason`），扩展既有 `hasLowConfidence`（观测路径）与 `TriggersForCaseResult`（评测集路径）两个低置信判定入口；风险排序是 domain 枚举 `ReviewRiskLevel` + `RiskLevel()` 纯映射 + persistence `ORDER BY CASE` 镜像表达式，`risk_level` 作为派生 JSON 字段在读取时填充，前端展示优先级标签。无 DDL、无 proto 变更（ReviewItem 是 domain JSON 契约，非 proto 生成）。

**Tech Stack:** Go 1.25（pgx v5 / Gin）、React 18 / AntD 5。

## Global Constraints

- **domain 只依赖 stdlib + pkg/constants**：`isBoundaryConfidence`/`hasVagueReason`/`RiskLevel` 都是纯函数；行为数字（边界 0.45/0.55、最短理由 rune 数、模糊词表）跨包放 `pkg/constants/evaluation.go`，模糊词表本身是硬编码控制逻辑，放 domain 包级 var。
- **控制逻辑硬编码**：风险优先级映射（trigger_reason → risk level）与 SQL ORDER BY 必须硬编码，AI 不决定排序；两处镜像必须互指注释 + 各自测试守护，禁止静默漂移。
- **无 DDL**：`risk_level` 派生自 `trigger_reason`，不新增列；ReviewItem JSON 新增字段，只影响 GET /evaluations/review 与 /:id 两个契约 golden。
- **Go 质量门禁**：圈复杂度≤10、认知≤15、行≤120、嵌套≤4；错误逐层 `fmt.Errorf("op: %w", err)`；测试表驱动。
- **前端**：唯一 Axios 实例 `client.ts`；用户可见文案中文；行为常量全大写下划线；组件 <200 行。
- **评审池触发语义不变**：边界/理由含糊只扩大「低置信」判定面（spec §6.6 明确要求），不改变其他触发、不改变 Decide 状态机、不改变 process_output_conflict。
- **契约 golden 守护**：改 ReviewItem JSON 后必须同步 `api/http/testdata/contracts/get_evaluations_review.golden.json` 与 `get_evaluations_review__id.golden.json`（`go test ./api/http/ -update` 规范化再生成，diff 只应新增 risk_level）。

---

### Task 1: 置信度机制边界（Go domain + constants + 单测）

**Files:**

- Modify: `pkg/constants/evaluation.go`（人工评审池 block，行 97-105 之后）
- Modify: `internal/evaluation/domain/review_pool.go`（imports + 新增纯函数 + 扩展两个低置信入口）
- Test: `internal/evaluation/domain/review_pool_test.go`

**Interfaces:**

- Consumes: `JudgeSignal{Dimension, Score, Confidence, Reason}`（Reason = 每维度打分理由，观测路径由 applyJudge 填充 `res.Message`）；`AssertionResult{Passed, Message, Confidence}`（Message = 整体判定理由，评测集路径）。
- Produces: `isBoundaryConfidence(c float64) bool`、`hasVagueReason(reason string) bool`（供 Task 2 无依赖，Task 1 内 self-contained）。

- [ ] **Step 1: 写失败测试**（review_pool_test.go 追加）

```go
func TestIsBoundaryConfidence(t *testing.T) {
	cases := []struct {
		name string
		conf float64
		want bool
	}{
		{"boundary low edge", 0.45, true},
		{"boundary midpoint", 0.50, true},
		{"boundary high edge", 0.55, true},
		{"below boundary", 0.44, false},
		{"above boundary", 0.56, false},
		{"normal high", 0.6, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBoundaryConfidence(tc.conf); got != tc.want {
				t.Fatalf("isBoundaryConfidence(%v) = %v, want %v", tc.conf, got, tc.want)
			}
		})
	}
}

func TestHasVagueReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"empty reason is vague", "", true},
		{"whitespace only is vague", "   ", true},
		{"too short reason is vague", "pass", true},
		{"hedging word is vague", "无法确定答案是否正确", true},
		{"maybe is vague", "可能正确", true},
		{"substantive reason is not vague", "输出完全符合预期，无任何偏差或遗漏", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVagueReason(tc.reason); got != tc.want {
				t.Fatalf("hasVagueReason(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}
```

并在 `TestTriggersForObservation` 追加（在既有 "rule conflict suppressed" 之后）：

```go
	t.Run("boundary confidence triggers low confidence", func(t *testing.T) {
		o := obs()
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.5, Reason: "理由充分"}}
		if got := TriggersForObservation(o, cfg); !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want low_confidence present", got)
		}
	})

	t.Run("vague reason triggers low confidence", func(t *testing.T) {
		o := obs()
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9, Reason: "不确定"}}
		if got := TriggersForObservation(o, cfg); !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want low_confidence present", got)
		}
	})

	t.Run("substantive reason and high confidence do not trigger", func(t *testing.T) {
		o := obs()
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9, Reason: "输出完全符合预期"}}
		if got := TriggersForObservation(o, cfg); containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want no low_confidence", got)
		}
	})
```

在 `TestTriggersForCaseResult` 追加（在 "both triggers coexist" 之后）：

```go
	t.Run("boundary confidence triggers low confidence", func(t *testing.T) {
		got := TriggersForCaseResult(false, true, true, AssertionResult{Passed: true, Confidence: 0.5}, cfg)
		if !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want low_confidence present", got)
		}
	})

	t.Run("vague overall reason triggers low confidence", func(t *testing.T) {
		got := TriggersForCaseResult(false, true, true, AssertionResult{Passed: true, Confidence: 0.9, Message: "无法判断"}, cfg)
		if !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want low_confidence present", got)
		}
	})

	t.Run("substantive message and high confidence do not trigger", func(t *testing.T) {
		got := TriggersForCaseResult(false, true, true, AssertionResult{Passed: true, Confidence: 0.9, Message: "输出完全符合预期"}, cfg)
		if containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want no low_confidence", got)
		}
	})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/domain/...`
Expected: FAIL——`isBoundaryConfidence`/`hasVagueReason` undefined；低置信新场景未触发。

- [ ] **Step 3: 加常量**（`pkg/constants/evaluation.go`，`ReviewBacklogAlertThreshold` 之后）

```go
// Evaluation 人工评审池置信度机制（§6.6 置信度机制：分数落边界或理由含糊也视为低置信）。
const (
	// ConfidenceBoundaryLow/High 界定 confidence 边界区间 [0.45, 0.55]：落在此区间的分数
	// 视为低置信（spec §6.6「分数落在边界(如 0.45–0.55)…也视为低置信」）。
	ConfidenceBoundaryLow  = 0.45
	ConfidenceBoundaryHigh = 0.55
	// VagueReasonMinRunes 打分理由视为含糊的最短有效 rune 数：理由为空或更短不足以支撑
	// 判定，视为含糊（spec §6.6「打分理由含糊也视为低置信」）。
	VagueReasonMinRunes = 8
)
```

- [ ] **Step 4: 实现 domain 纯函数**（`review_pool.go`）

imports 改为：

```go
import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)
```

在 `hasLowConfidence` 函数之后追加：

```go
// vagueReasonKeywords 是打分理由含糊的硬编码判据（spec §6.6：理由含不确定性措辞视为含糊）。
// 规则断言天然确定，不参与——本判定仅作用于 judge 信号。
var vagueReasonKeywords = []string{
	"不确定", "无法确定", "无法判断", "不能判断", "不清楚", "可能", "也许", "大概", "似乎",
}

// isBoundaryConfidence 判定 confidence 是否落在边界区间 [ConfidenceBoundaryLow, ConfidenceBoundaryHigh]
// （spec §6.6：分数落在边界视为低置信）。
func isBoundaryConfidence(c float64) bool {
	return c >= constants.ConfidenceBoundaryLow && c <= constants.ConfidenceBoundaryHigh
}

// hasVagueReason 判定打分理由是否含糊：为空/过短（< VagueReasonMinRunes rune），或含不确定性措辞。
// spec §6.6「打分理由含糊也视为低置信」。
func hasVagueReason(reason string) bool {
	if strings.TrimSpace(reason) == "" {
		return true
	}
	if utf8.RuneCountInString(reason) < constants.VagueReasonMinRunes {
		return true
	}
	for _, kw := range vagueReasonKeywords {
		if strings.Contains(reason, kw) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: 扩展两个低置信入口**（`review_pool.go`）

`hasLowConfidence` 改为：

```go
// hasLowConfidence 返回任一 judge 维度满足低置信：Confidence < threshold、落在边界区间，
// 或打分理由含糊（spec §6.6 置信度机制）。
func hasLowConfidence(judge []JudgeSignal, threshold float64) bool {
	for _, j := range judge {
		if j.Confidence < threshold || isBoundaryConfidence(j.Confidence) || hasVagueReason(j.Reason) {
			return true
		}
	}
	return false
}
```

`TriggersForCaseResult` 的低置信分支改为：

```go
	if assertion.Confidence < cfg.LowConfidenceThreshold ||
		isBoundaryConfidence(assertion.Confidence) ||
		hasVagueReason(assertion.Message) {
		triggers = append(triggers, TriggerLowConfidence)
	}
```

- [ ] **Step 6: 运行测试确认通过 + 全量 domain 测试**

Run: `go test ./internal/evaluation/domain/...`
Expected: PASS（含既有 TestTriggersForObservation/TestTriggersForCaseResult，验证未破坏原触发）。

- [ ] **Step 7: commit**

```bash
git add pkg/constants/evaluation.go internal/evaluation/domain/review_pool.go internal/evaluation/domain/review_pool_test.go
git commit -m "feat(evaluation): 置信度机制边界——边界分数/理由含糊视为低置信（§6.6 补齐）"
```

---

### Task 2: 评审池按风险排序（Go domain + persistence + 契约 golden）

**Files:**

- Modify: `internal/evaluation/domain/review_pool.go`（ReviewRiskLevel 枚举 + RiskLevel() + ReviewItem.RiskLevel 字段）
- Modify: `internal/evaluation/infrastructure/persistence/review_repository.go`（reviewRiskOrderSQL + ListItems ORDER BY + Get/List 填充 RiskLevel）
- Test: `internal/evaluation/domain/review_pool_test.go`、`internal/evaluation/infrastructure/persistence/review_repository_test.go`
- Modify: `api/http/testdata/contracts/get_evaluations_review.golden.json`、`get_evaluations_review__id.golden.json`

**Interfaces:**

- Consumes: `ReviewTriggerReason` 五个枚举、`ReviewItem`（现有字段）、`ReviewFilter`、`reviewFilterConds`。
- Produces: `type ReviewRiskLevel string`（high/medium/low）、`func (r ReviewTriggerReason) RiskLevel() ReviewRiskLevel`、`ReviewItem.RiskLevel ReviewRiskLevel \`json:"risk_level"\``、`func reviewRiskOrderSQL() string`（persistence 包私有）。

- [ ] **Step 1: 写失败测试**（review_pool_test.go 追加）

```go
func TestReviewTriggerReasonRiskLevel(t *testing.T) {
	cases := []struct {
		reason ReviewTriggerReason
		want   ReviewRiskLevel
	}{
		{TriggerJudgeRuleConflict, ReviewRiskHigh},
		{TriggerProcessOutputConflict, ReviewRiskHigh},
		{TriggerLowConfidence, ReviewRiskMedium},
		{TriggerDimensionSplit, ReviewRiskMedium},
		{TriggerNeedsReview, ReviewRiskMedium},
		{ReviewTriggerReason("unknown_future"), ReviewRiskLow},
	}
	for _, tc := range cases {
		t.Run(string(tc.reason), func(t *testing.T) {
			if got := tc.reason.RiskLevel(); got != tc.want {
				t.Fatalf("RiskLevel(%q) = %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/domain/...`
Expected: FAIL——`ReviewRiskLevel`/`RiskLevel` undefined。

- [ ] **Step 3: 实现 domain 枚举与映射**（`review_pool.go`，ReviewTriggerReason.Valid 之后）

```go
// ReviewRiskLevel 评审优先级（spec §6.6 规模控制：评审池按风险排序，安全/写操作/高危资源优先）。
type ReviewRiskLevel string

const (
	ReviewRiskHigh   ReviewRiskLevel = "high"
	ReviewRiskMedium ReviewRiskLevel = "medium"
	ReviewRiskLow    ReviewRiskLevel = "low"
)

// RiskLevel 把入池原因映射为评审优先级。硬编码规则，与 persistence 的 reviewRiskOrderSQL
// 保持镜像（两端注释互指，修改必须同步）：
//   - high：judge_rule_conflict（规则护栏命中 = 安全类）、process_output_conflict（副作用/写操作越界）；
//   - medium：low_confidence、dimension_split、needs_review；
//   - low：其余（未来新增触发默认低，人工可随时介入）。
func (r ReviewTriggerReason) RiskLevel() ReviewRiskLevel {
	switch r {
	case TriggerJudgeRuleConflict, TriggerProcessOutputConflict:
		return ReviewRiskHigh
	case TriggerLowConfidence, TriggerDimensionSplit, TriggerNeedsReview:
		return ReviewRiskMedium
	default:
		return ReviewRiskLow
	}
}
```

`ReviewItem` 结构在 `TriggerReason` 字段之后追加：

```go
	TriggerReason ReviewTriggerReason `json:"trigger_reason"`
	// RiskLevel 评审优先级（派生自 trigger_reason；不落库，repository 读取后填充，
	// JSON 透出供前端展示排序依据）。
	RiskLevel ReviewRiskLevel `json:"risk_level"`
```

- [ ] **Step 4: 实现 persistence 排序与填充**（`review_repository.go`）

ListItems 的 listSQL 行（当前行 102-106）改为：

```go
	listSQL := `SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id,
	                   trigger_reason, snapshot, status, human_verdict, reviewer, review_reason,
	                   created_at, reviewed_at
	            FROM eval_review_items` + conds +
		fmt.Sprintf(` ORDER BY %s, created_at DESC LIMIT $%d OFFSET $%d`,
			reviewRiskOrderSQL(), len(args)+1, len(args)+2)
```

在 `reviewFilterConds` 函数之后追加：

```go
// reviewRiskOrderSQL 是评审池风险优先排序表达式（spec §6.6 规模控制：评审池按风险排序，
// 安全/写操作/高危资源优先）。与 domain.ReviewTriggerReason.RiskLevel() 保持镜像：
// high=0、medium=1、low=2；同风险按 created_at DESC（维持既有最新优先）。修改 RiskLevel()
// 必须同步本表达式（两端注释互指）。
func reviewRiskOrderSQL() string {
	return `CASE trigger_reason WHEN 'judge_rule_conflict' THEN 0 WHEN 'process_output_conflict' THEN 0 WHEN 'low_confidence' THEN 1 WHEN 'dimension_split' THEN 1 WHEN 'needs_review' THEN 1 ELSE 2 END`
}
```

GetItem（当前行 87 `item.TriggerReason = domain.ReviewTriggerReason(trigger)` 之后）追加：

```go
	item.RiskLevel = item.TriggerReason.RiskLevel()
```

ListItems 循环内（当前行 145 `item.TriggerReason = domain.ReviewTriggerReason(trigger)` 之后）追加：

```go
			item.RiskLevel = item.TriggerReason.RiskLevel()
```

- [ ] **Step 5: 更新 persistence 单测**（`review_repository_test.go`）

ListItems 测试（`TestPgReviewRepositoryListItems`，行 163）的期望查询字符串改为：

```go
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id, trigger_reason, snapshot, status, human_verdict, reviewer, review_reason, created_at, reviewed_at FROM eval_review_items WHERE 1=1 ORDER BY CASE trigger_reason WHEN 'judge_rule_conflict' THEN 0 WHEN 'process_output_conflict' THEN 0 WHEN 'low_confidence' THEN 1 WHEN 'dimension_split' THEN 1 WHEN 'needs_review' THEN 1 ELSE 2 END, created_at DESC LIMIT $1 OFFSET $2`)).
```

并在该测试的行扫描断言处追加 RiskLevel 填充断言：若 fixture 行的 trigger_reason 是 `low_confidence`，断言 `items[0].RiskLevel == domain.ReviewRiskMedium`（先读该测试现有 fixture 与断言，保持风格）。同时给 GetItem 测试（若存在）补同款 RiskLevel 断言。

Run: `go test ./internal/evaluation/domain/... ./internal/evaluation/infrastructure/persistence/...`
Expected: PASS。

- [ ] **Step 6: 同步契约 golden**

Run: `go test ./api/http/ -update`（规范化再生成，仿既有 golden 同步惯例）
然后 `git diff api/http/testdata/contracts/` 检查：两个 review golden 应只新增 `"risk_level": "medium"`（在 `"trigger_reason": "low_confidence"` 之后），其余 golden 不得变化。若 -update 生成多余改动，手工回退非目标文件。

Run: `go test ./api/http/...`
Expected: PASS（契约 golden 校验通过）。

- [ ] **Step 7: commit**

```bash
git add internal/evaluation/domain/review_pool.go internal/evaluation/domain/review_pool_test.go internal/evaluation/infrastructure/persistence/review_repository.go internal/evaluation/infrastructure/persistence/review_repository_test.go api/http/testdata/contracts/
git commit -m "feat(evaluation): 评审池按风险排序——安全/写操作优先（§6.6 补齐）"
```

---

### Task 3: 前端优先级标签（React）

**Files:**

- Modify: `web/src/modules/evaluation/types/review.ts`
- Modify: `web/src/modules/evaluation/components/ReviewPoolPanel.tsx`
- Test: `web/src/modules/evaluation/components/ReviewPoolPanel.test.tsx`

**Interfaces:**

- Consumes: 后端 GET /evaluations/review 返回 `ReviewItem`（Task 2 新增 `risk_level` 字段）。
- Produces: 无跨任务依赖（本任务最后一项）。

- [ ] **Step 1: 写失败测试**（`ReviewPoolPanel.test.tsx`）

`pendingItem` fixture 追加字段：`risk_level: 'medium',`（trigger_reason 是 low_confidence → medium）。

新增用例（放在既有渲染断言之后）：

```tsx
  it('renders risk priority label from backend', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel />);

    expect(await screen.findByText('中')).toBeInTheDocument();
  });
```

Run: `cd web && npx vitest run src/modules/evaluation/components/ReviewPoolPanel.test.tsx`
Expected: FAIL——`中` 标签不存在（列未实现）。

- [ ] **Step 2: 前端类型加字段**（`types/review.ts`）

`ReviewItem` 接口在 `trigger_reason: string;` 之后追加：

```ts
  trigger_reason: string;
  risk_level?: string;
```

- [ ] **Step 3: 面板加优先级列**（`ReviewPoolPanel.tsx`）

在 `REASON_LABELS` 定义之后追加风险标签映射：

```tsx
const RISK_LABELS: Record<string, string> = { high: '高', medium: '中', low: '低' };
const RISK_COLORS: Record<string, string> = { high: 'red', medium: 'orange', low: 'blue' };
```

在 columns 的「状态」列之前插入：

```tsx
    { title: '优先级', dataIndex: 'risk_level', width: 90,
      render: (v: string) => (v ? <Tag color={RISK_COLORS[v]}>{RISK_LABELS[v] ?? v}</Tag> : '-') },
```

Drawer 详情首行追加优先级展示（当前行 122）：

```tsx
              <div>来源：{detail.source_type === 'observation' ? '观测' : '评测集'} · 原因：{REASON_LABELS[detail.trigger_reason] ?? detail.trigger_reason} · 优先级：{detail.risk_level ? RISK_LABELS[detail.risk_level] : '-'}</div>
```

- [ ] **Step 4: 运行测试 + tsc**

Run: `cd web && npx vitest run src/modules/evaluation/components/ReviewPoolPanel.test.tsx && npx tsc --noEmit`
Expected: PASS + tsc clean。

- [ ] **Step 5: commit**

```bash
git add web/src/modules/evaluation/types/review.ts web/src/modules/evaluation/components/ReviewPoolPanel.tsx web/src/modules/evaluation/components/ReviewPoolPanel.test.tsx
git commit -m "feat(evaluation): 评审池前端展示风险优先级标签（§6.6 补齐）"
```

---

## 验证清单

| 层 | 命令 |
|---|---|
| domain | `go test -race ./internal/evaluation/domain/...` |
| persistence | `go test -race ./internal/evaluation/infrastructure/persistence/...` |
| contract | `go test ./api/http/...`（含 review golden 校验） |
| 前端 | `cd web && npx tsc --noEmit && npx vitest run src/modules/evaluation` |
| 全量 Go | `go vet ./... && go test -short ./...` |
| 最终 | 全绿后 clean commit → stratum-e2e-tester 系统验收（R2 e2e-short；按 .test/verification.yaml 定级）→ push → PR → CI → merge → CD 部署验证 |

## 主要风险与对策

1. **空 Reason 扩大触发面**：观测路径若 judge 某维度没给理由，`hasVagueReason("")`=true → 入池。这是 spec §6.6 明确意图（理由含糊/缺失视为低置信），非缺陷；评审池有积压告警 + 风险排序兜底。
2. **domain 与 SQL 两处映射漂移** → 两端注释互指 + domain 单测（RiskLevel 全枚举）+ pgxmock 精确 SQL 断言双重守护。
3. **契约 golden 破坏** → 只新增 risk_level 字段，`go test ./api/http/ -update` 后 diff 审查仅限两个 review golden；Task 3 前端类型同 PR 一并合入。
4. **RiskLevel JSON 字段未填充** → repository Get/List 扫描后统一填充（Upsert 不返回条目，无 JSON 输出路径），并加单测断言。

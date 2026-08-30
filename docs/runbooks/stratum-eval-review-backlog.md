<a id="stratum-eval-review-backlog-high"></a>

## StratumEvalReviewBacklogHigh

人工评审池积压告警处置手册。

**告警含义**：人工评审池待评审条目 `eval_review_backlog` 持续超过 50（15 分钟）。
**影响**：低置信/判异样本滞留评审池，judge 误判与产品缺陷无法及时回写。
**排查**：

1. `SELECT count(*) FROM eval_review_items WHERE status = 'pending';`（逐租户）
2. 按 `trigger_reason` 分组看是否单一原因堆积：观测 vs 评测集。
3. 若 `low_confidence` 大量堆积：检查 judge 模型质量/温度参数，评估置信度阈值。
4. 若 `judge_rule_conflict` 堆积：规则护栏与 judge 判定系统性冲突，需人工抽查。
**处置**：admin 进评审池逐条决策；积压持续时评估阈值或扩充评审人力。

> **阈值对齐**：告警 expr 中硬编码的 `50`（promql 无法引用 Go 常量）与
> `pkg/constants/evaluation.go` 的 `ReviewBacklogAlertThreshold` 常量保持一致；
> 调整阈值时必须同步这两处，避免漂移。

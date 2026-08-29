# Stratum Evaluation 告警 Runbook

评测运行态观测层（Phase 1）告警处置手册。

## 通用处置

- 所有判异类告警先到评测中心（运行态观测视图）按 trace 下钻，确认是否为真实能力退化。
- 告警只做通知与定位，处置动作（回滚/调参/拦截规则调整）走既有操作台与 CD 流程，禁止远端手改。
- 规则护栏（T1/T4）命中即拦截，属 fail closed 预期行为；持续命中说明平台规则需重新评估。

<a id="stratum-eval-behavior-anomaly"></a>

## StratumEvalBehaviorAnomaly

行为异常判异速率升高（judge 跌阈 / 行为放弃 / 行为升级）。

- 定位：查询 `eval_behavior_anomaly_total` 按 signal 分组，定位具体异常维度与 resource。
- 确认：到评测中心下钻对应 trace，核对 judge 分数与行为信号来源（feedback 或 agent 埋点）。
- 处置：真能力退化 → 走参数/版本调整；误报 → 调整判异阈值或信号映射。

<a id="stratum-eval-sample-coverage-low"></a>

## StratumEvalSampleCoverageLow

主动采样覆盖率低于阈值，观测落库可能被静默跳过。

- 语义：`eval_sample_coverage` = 落库观测 / 采样候选（采样通过且 judge 开启）。健康稳态 ≈1.0；
  judge 配置关闭（主动停观测）不计入分母、不触发本告警。
- 定位：先区分「主动停观测」与「故障降级」——核对 `evaluation.observe.enabled` / `evaluation.observe.sample_rate` 与 judge 可用性；
  覆盖率掉低但 judge 正常时，查落库链路（Validate 失败 / Save 失败）与 `eval_judge_failure_total` 的 `reason` 维度。
- 处置：恢复 judge 或修复落库链路；覆盖率长期低说明观测失去代表性（§14 禁止静默跳过某层）。

<a id="stratum-eval-rule-blocked"></a>

## StratumEvalRuleBlocked

规则护栏即时拦截命中（T4 红线级）。

- 定位：按 `rule` label 定位命中规则与工具；查询 `eval_rule_hit_total` 按 tool 分布。
- 处置：T4 强制人工确认——评估该工具是否应继续禁用、denylist 是否需调整，经审批后在平台参数更新
  `evaluation.ruleguard.denylist`，再回归验证。禁止自动放行（fail closed，§14）。

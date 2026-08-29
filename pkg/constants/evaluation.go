// Package constants — evaluation tunable bounds.
package constants

import "time"

// Worker polling bounds.
const (
	// EvaluationIdleInterval 是评估 worker 无待办 job/实验时的空转等待：
	// 空队列时按该间隔轮询，禁止每租户每秒空查（2026-08-21 CPU 打满事故）。
	EvaluationIdleInterval = 2 * time.Second

	// 运行态观测（P1a，§4.2）引用事件与消费链路常量。
	// ObservationStream 承载运行态观测引用事件（WorkQueue 单消费语义）。
	ObservationStream = "evaluation-observe"
	// ObservationDLQStream 观测消费重投耗尽后的死信流。
	ObservationDLQStream = "evaluation-observe-dlq"
	// ObservationSubjectPrefix 引用事件 subject 前缀；完整 subject 为
	// "evaluation.observe.{tenant}"，与 memory 的 domain.action 命名同族。
	ObservationSubjectPrefix = "evaluation.observe"
	// ObservationDLQSubject 观测死信流独立 subject。独立前缀避免与观察流通配
	// "evaluation.observe.>" 重叠（否则死信消息按 subject 同时落入两流，观察
	// consumer 重消费死信导致重投死循环，仿 memory.dlq 的不相交前缀模式）。
	// DLQ 流按精确 subject 匹配，租户写入事件字段。
	ObservationDLQSubject = "evaluation.dlq"
	// ObservationConsumerName 观测 judge 消费组名。
	ObservationConsumerName = "observation-judge"
	// ObservationAckWait 消费确认窗口；ObservationMaxDeliver 重投上限，超限进 DLQ。
	ObservationAckWait    = 30 * time.Second
	ObservationMaxDeliver = 3
	// ObservationFetchMaxWait 单次 Fetch 等待窗口；ObservationFetchBackoffBase /
	// ObservationFetchBackoffMax 消费退避与重投延迟的指数区间（重投 NakWithDelay 用 Base）。
	ObservationFetchMaxWait     = 5 * time.Second
	ObservationFetchBackoffBase = 1 * time.Second
	ObservationFetchBackoffMax  = 30 * time.Second
	// ObservationStreamMaxAge / ObservationDLQMaxAge 消息保留期。
	ObservationStreamMaxAge = 7 * 24 * time.Hour
	ObservationDLQMaxAge    = 30 * 24 * time.Hour
	// ObservationSampleRateDefault 采样率默认值（registry 兜底，运行时经平台参数覆盖）。
	ObservationSampleRateDefault = 0.1
	// ObservationPublishTimeout 引用事件发布超时预算（agent 侧 best-effort，超时不阻断）。
	ObservationPublishTimeout = 3 * time.Second
	// ObservationBacklogInterval 消费积压指标采集周期。
	ObservationBacklogInterval = 30 * time.Second
)

// Tunable parameter bounds shared by the evaluation domain (tunable
// registration), the AgentRevision model validation, and the evaluation
// Agent adapter. Zero means "unset": a candidate may always express unset
// to leave the production value untouched.
const (
	// TunableTemperatureMin/Max bound the LLM temperature parameter.
	// Max is 1.0, not 2.0: the platform's OpenAI-compatible providers (Qwen /
	// Zhipu) reject temperature > 1 with a 4xx that the gateway surfaces as
	// 500 at execution. 1.0 also matches evaluation.optimizer/judge.temperature.
	TunableTemperatureMin = 0.0
	TunableTemperatureMax = 1.0

	// TunableMaxTokensMin/Max bound max_tokens per LLM request. Min is 0 (unset).
	TunableMaxTokensMin = 0
	TunableMaxTokensMax = 131072

	// TunableMaxContextTokensMin/Max/Step bound the context memory window
	// slider exposed to optimization (0 = auto-derive from the model).
	TunableMaxContextTokensMin  = 0
	TunableMaxContextTokensMax  = 32768
	TunableMaxContextTokensStep = 1024

	// TunableRecentGroupsMax caps compaction_recent_groups.
	TunableRecentGroupsMax = 5

	// JudgeMaxTokens caps a single LLM judge response; a verdict is a short
	// JSON object, so a fixed cap keeps judge cost bounded regardless of
	// provider. The judge itself is gated by evaluation.judge.enabled.
	JudgeMaxTokens = 1024

	// CaseGenMaxTokens caps a single case-generator response: one eval case
	// JSON object (name/input/expected_output/assertion_mode/reason).
	CaseGenMaxTokens = 2048

	// DefaultCaseSampleLimit bounds how many production samples one
	// generation pass samples when the caller does not request more.
	DefaultCaseSampleLimit = 20

	// MaxCaseSampleLimit caps the caller-provided sample count so one
	// request cannot fan out unbounded LLM calls.
	MaxCaseSampleLimit = 50
)

// Evaluation 运行态观测行为阈值（P1b §4.2）。
const (
	// JudgeBelowThreshold 是 judge 单维度跌阈判异的阈值（§4.2 判异触发）。
	JudgeBelowThreshold = 0.5
	// FeedbackNegativeThreshold 是 feedback 负反馈判异阈值：score 低于该值视为放弃倾向。
	FeedbackNegativeThreshold = 0.5
)

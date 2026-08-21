// Package constants — evaluation tunable bounds.
package constants

import "time"

// Worker polling bounds.
const (
	// EvaluationIdleInterval 是评估 worker 无待办 job/实验时的空转等待：
	// 空队列时按该间隔轮询，禁止每租户每秒空查（2026-08-21 CPU 打满事故）。
	EvaluationIdleInterval = 2 * time.Second
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

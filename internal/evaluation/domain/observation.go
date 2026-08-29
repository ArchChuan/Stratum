package domain

import (
	"fmt"
	"time"
)

// ObservationVerdict 是 EvalObservation 的仅信号级结论（§4.3），非权威判定。
type ObservationVerdict string

const (
	VerdictPass  ObservationVerdict = "pass"
	VerdictFlag  ObservationVerdict = "flag"
	VerdictBlock ObservationVerdict = "block"
)

// ParamSource 标记关键参数实际生效的来源层级（§4.3）。
type ParamSource string

const (
	ParamSourcePlatform ParamSource = "platform"
	ParamSourceResource ParamSource = "resource"
	ParamSourceBoth     ParamSource = "both"
	ParamSourceUnknown  ParamSource = "unknown"
)

// PlatformParamVersion 平台层生效版本锚点。P1a 阶段配置版本机制未绑定，
// 统一标记 unknown；Phase 2 绑定后回填。
type PlatformParamVersion struct {
	GroupKey   string `json:"group_key"`
	VersionSeq int64  `json:"version_seq"`
}

// ResourceParamVersion 租户资源配置版本锚点（来自执行证据的 Assignments）。
type ResourceParamVersion struct {
	Ref     string `json:"ref"`
	Version string `json:"version"`
}

// ParamVersion 双版本锚点（§4.3/§3.4）。
type ParamVersion struct {
	Platform PlatformParamVersion `json:"platform"`
	Resource ResourceParamVersion `json:"resource"`
	Source   ParamSource          `json:"source"`
}

// RuleSignal 规则护栏命中信号（P1b 填充）。
type RuleSignal struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// JudgeSignal LLM judge 单维度打分（score/confidence 归一化到 [0,1]）。
type JudgeSignal struct {
	Dimension  string  `json:"dimension"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
}

// BehaviorSignals 用户行为信号（P1b 填充）。
type BehaviorSignals struct {
	Retry       bool `json:"retry"`
	Escalation  bool `json:"escalation"`
	Abandonment bool `json:"abandonment"`
}

// ObservationSignals 观测信号集合。
type ObservationSignals struct {
	Rule     []RuleSignal    `json:"rule"`
	Judge    []JudgeSignal   `json:"judge"`
	Behavior BehaviorSignals `json:"behavior"`
}

// CostPerf 成本性能。
type CostPerf struct {
	LatencyMS int64   `json:"latency_ms"`
	Tokens    int64   `json:"tokens"`
	CostUSD   float64 `json:"cost_usd"`
}

// ObservationResourceRef 观测对象资源锚点（kind=agent 时 ResourceID 为 agent_id）。
// 与领域 ResourceRef（含 revision_id）不同：观测锚点只含 kind + resource_id，
// revision 语义由 param_version.resource 承载（spec §4.3 resource(kind,id)）。
type ObservationResourceRef struct {
	Kind       ResourceKind `json:"kind"`
	ResourceID string       `json:"resource_id"`
}

// EvalObservation 一次运行态观测（规格 §4.3）。
type EvalObservation struct {
	ID        string                 `json:"id"`
	TraceID   string                 `json:"trace_id"`
	Resource  ObservationResourceRef `json:"resource"`
	Param     ParamVersion           `json:"param_version"`
	Signals   ObservationSignals     `json:"signals"`
	CostPerf  CostPerf               `json:"cost_perf"`
	Stratum   string                 `json:"stratum"`
	Verdict   ObservationVerdict     `json:"verdict"`
	CreatedAt time.Time              `json:"created_at"`
}

// Validate 校验观测字段，非法返回错误。fail closed：不允许坏数据落库。
func (o *EvalObservation) Validate() error {
	if o.TraceID == "" {
		return fmt.Errorf("evaluation observation: trace_id required")
	}
	if o.Resource.Kind == "" {
		return fmt.Errorf("evaluation observation: resource kind required")
	}
	if o.Resource.ResourceID == "" {
		return fmt.Errorf("evaluation observation: resource id required")
	}
	switch o.Verdict {
	case VerdictPass, VerdictFlag, VerdictBlock:
	default:
		return fmt.Errorf("evaluation observation: invalid verdict %q", o.Verdict)
	}
	for _, j := range o.Signals.Judge {
		if err := j.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validate 校验单条 judge 信号，按 brief 顺序：dimension → score → confidence。
func (j JudgeSignal) validate() error {
	if j.Dimension == "" {
		return fmt.Errorf("evaluation observation: judge dimension required")
	}
	if j.Score < 0 || j.Score > 1 {
		return fmt.Errorf("evaluation observation: judge score %v out of [0,1]", j.Score)
	}
	if j.Confidence < 0 || j.Confidence > 1 {
		return fmt.Errorf("evaluation observation: judge confidence %v out of [0,1]", j.Confidence)
	}
	return nil
}

package port

import "context"

// ObservationEvent 是 agent 执行成功后发布的轻量引用事件（规格 §2.2）。
// 只携带 trace 标识与资源锚点，证据本体（Opik）由评测服务拉取，禁止在
// 事件里携带 prompt/输出等 payload（观测埋点只做引用、不做 payload 双写）。
type ObservationEvent struct {
	TenantID     string `json:"tenant_id"`
	TraceID      string `json:"trace_id"`
	ExecutionID  string `json:"execution_id"`
	AgentID      string `json:"agent_id"`
	ResourceKind string `json:"resource_kind"`
	ResourceID   string `json:"resource_id"`
	// CompletedAt RFC3339Nano 时间戳，评测侧解析为创建时间锚点。
	CompletedAt string `json:"completed_at"`
}

// ObservationEmitter 发布观测引用事件。实现必须 best-effort：失败只应记录
// 日志，绝不阻断 agent 执行（评估器不阻断执行铁律）。
type ObservationEmitter interface {
	Emit(ctx context.Context, evt ObservationEvent) error
}

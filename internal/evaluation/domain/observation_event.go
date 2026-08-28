package domain

// ObservationReferenceEvent 是评测侧消费的观测引用事件解析结构，字段与
// internal/agent/domain/port 的 ObservationEvent 逐一对应（由
// observation_event_test.go 的 golden 契约守护对齐）。
type ObservationReferenceEvent struct {
	TenantID     string `json:"tenant_id"`
	TraceID      string `json:"trace_id"`
	ExecutionID  string `json:"execution_id"`
	AgentID      string `json:"agent_id"`
	ResourceKind string `json:"resource_kind"`
	ResourceID   string `json:"resource_id"`
	CompletedAt  string `json:"completed_at"`
}

// ResourceRef 返回该事件的资源锚点（agent 执行观测对象，ObservationResourceRef）。
func (e ObservationReferenceEvent) ResourceRef() ObservationResourceRef {
	return ObservationResourceRef{Kind: ResourceKind(e.ResourceKind), ResourceID: e.ResourceID}
}

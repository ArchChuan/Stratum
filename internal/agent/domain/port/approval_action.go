package port

import "context"

// ApprovalActionRequest 描述一次审批通过后的动作执行请求（D3 执行器分发）。
type ApprovalActionRequest struct {
	TenantID    string
	SubjectKind string
	Arguments   map[string]any
	ActorID     string // 发起人（审计原真）
	DecidedBy   string // 审批人（执行权限：mcp 配置类 ownership 校验以此为准）
}

// ApprovalActionExecutor 由 wiring 装配，把 subject_kind 分发到对应 context 的 service。
type ApprovalActionExecutor interface {
	ExecuteApprovalAction(ctx context.Context, req ApprovalActionRequest) (map[string]any, error)
}

// ApprovalActionNotExecutedError 表示动作在产生任何副作用前失败（预执行失败，如
// 目标不可达、参数校验失败），可安全重试——service 收到后把审批释放回 approved。
// 产生副作用后失败的执行器必须返回普通 error，service 将审批标记为 unknown_outcome。
type ApprovalActionNotExecutedError struct{ Err error }

func (e *ApprovalActionNotExecutedError) Error() string { return e.Err.Error() }
func (e *ApprovalActionNotExecutedError) Unwrap() error { return e.Err }

package port

import (
	"context"
	"errors"
)

// ResourceFailure 是一次失败的租户资源操作（创建/更新/发布/连接等）。只记录
// 安全摘要：ErrorCode 是短错误类别，Detail 不得包含凭据、URL 或原始请求体。
type ResourceFailure struct {
	ResourceKind string
	ResourceID   string
	Operation    string // 原始意图：create / update / publish / connect / delete
	ErrorCode    string // 短错误类别：transport / validation / conflict / upstream / unknown
	Detail       string // 可选安全摘要，必须经过脱敏
}

// FailureAuditRecorder 记录失败的资源操作到租户 resource_change_audits。
// 实现必须 fail-open：写入失败只返回错误供调用方记录日志，绝不改变主流程结果。
type FailureAuditRecorder interface {
	Record(ctx context.Context, f ResourceFailure) error
}

// ClassifyFailure 把错误归类为短错误类别。仅供审计投影使用，不参与业务判断。
func ClassifyFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "unknown"
	}
}

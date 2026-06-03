// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"fmt"
	"time"
)

// ErrorCode 统一错误码
type ErrorCode int

const (
	ErrSkillNotFound      ErrorCode = 4001
	ErrSkillTimeout       ErrorCode = 4002
	ErrSkillAlreadyExists ErrorCode = 4003
	ErrSkillExecFailed    ErrorCode = 5001
	ErrCircuitOpen        ErrorCode = 5002
	ErrPipelineStepFailed ErrorCode = 5003
)

// SkillError 统一错误类型
type SkillError struct {
	Code    ErrorCode
	Message string
	TraceID string
	Cause   error
}

func (e *SkillError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%d] %s (trace_id=%s): %v", e.Code, e.Message, e.TraceID, e.Cause)
	}
	return fmt.Sprintf("[%d] %s (trace_id=%s)", e.Code, e.Message, e.TraceID)
}

func (e *SkillError) Unwrap() error {
	return e.Cause
}

// RetryConfig 重试配置
type RetryConfig struct {
	MaxAttempts int           // 0 = 不重试
	BaseDelay   time.Duration // 指数退避基础延迟，默认 100ms
}

// SkillRequest 统一请求结构
type SkillRequest struct {
	TraceID  string        // 若为空则自动生成 UUID
	SkillID  string        // 必填
	Input    any           // skill 输入，由 provider 解析
	Timeout  time.Duration // 0 表示使用默认值 30s
	Retry    RetryConfig
	Metadata map[string]string
}

// SkillResponse 统一响应结构
type SkillResponse struct {
	TraceID  string
	SkillID  string
	Output   any
	Duration time.Duration
	Metadata map[string]string
}

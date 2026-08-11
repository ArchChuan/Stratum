package infrastructure

import (
	"context"
	"errors"
	"net"
	"strings"
)

// permanentError 标记不应被上层重试机制（如 agent 层 RetryFn）重放的错误。
// 通过 Permanent() 方法被消费方以接口探测识别（鸭子类型，避免跨 context import）。
type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent 实现 permanentMarker 接口，供上层重试层判定。
func (e *permanentError) Permanent() bool { return true }

// markPermanent 把 err 包装为 permanent 错误（保留 errors.Is/As 语义）。
func markPermanent(err error) error {
	if err == nil || IsPermanent(err) {
		return err
	}
	return &permanentError{err: err}
}

// IsPermanent 报告 err 链中是否含 permanent 标记。
func IsPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}

// ErrContextLengthExceeded 表示请求超出模型上下文窗口：不可恢复（重试依旧
// 报错），agent 层可感知并触发降级/下修闭环。
var ErrContextLengthExceeded = &contextLengthExceededError{msg: "context length exceeded"}

type contextLengthExceededError struct{ msg string }

func (e *contextLengthExceededError) Error() string               { return e.msg }
func (e *contextLengthExceededError) Unwrap() error               { return nil }
func (e *contextLengthExceededError) Permanent() bool             { return true } // permanentMarker
func (e *contextLengthExceededError) ContextLengthExceeded() bool { return true }

// IsContextLengthExceeded 报告 err（含包装链）是否为上下文超限。
func IsContextLengthExceeded(err error) bool {
	return errors.Is(err, ErrContextLengthExceeded)
}

// isTransient 分类 LLM provider 调用错误：瞬态错误可触发 fallback 降级；
// 永久错误（含 context.Canceled、DeadlineExceeded）立即停止链，保持 fail-fast 语义。
//
// 瞬态：429、5xx、网络层超时、连接错误。
// 永久：context.Canceled、context.DeadlineExceeded、其他 4xx、解析/校验类错误。
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	// fail-fast：取消永不降级，避免在用户已离开时继续消费上游。
	if errors.Is(err, context.Canceled) {
		return false
	}
	// DeadlineExceeded 是永久错误：等待无意义，继续试只叠加时延。
	// 必须放在 isNetTransient 之前：http.Client timeout 的错误链
	// (url.Error → net.OpError → DeadlineExceeded) 会被 isNetTransient
	// 误判为网络瞬态，导致压缩预算耗尽后 gateway 空转重试。
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isNetTransient(err) {
		return true
	}
	if isConnError(err) {
		return true
	}
	return isStatusTransient(err)
}

// isNetTransient 判定超时与网络层错误（net.Error.Timeout、net.OpError）。
func isNetTransient(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// isConnError 按错误文本匹配连接级故障（provider 无类型化错误时兜底）。
func isConnError(err error) bool {
	return isConnErrorText(strings.ToLower(err.Error()))
}

// isConnErrorText 匹配连接错误常见文本片段。
func isConnErrorText(msg string) bool {
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "tls handshake")
}

// isStatusTransient 判定 HTTP 状态码瞬态（429 / 5xx）。
func isStatusTransient(err error) bool {
	var status interface{ StatusCode() int }
	if errors.As(err, &status) {
		code := status.StatusCode()
		return code == 429 || code >= 500
	}
	return false
}

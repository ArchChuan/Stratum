package graph

import (
	"context"
	"errors"
	"time"
)

// permanentMarker 由下游实现（如 llmgateway 的 permanent 错误）标记
// 不值得重试的错误：fallback 链已耗尽或错误为永久性时，重试只会放大
// 上游调用次数。通过方法探测鸭子类型识别，避免跨包类型依赖。
type permanentMarker interface {
	Permanent() bool
}

// RetryConfig controls retry behaviour.
type RetryConfig struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultRetry is 3 attempts, 100ms base, 10s max.
var DefaultRetry = RetryConfig{Attempts: 3, Base: 100 * time.Millisecond, Max: 10 * time.Second}

// RetryFn calls fn up to cfg.Attempts times with exponential backoff.
// Returns the first successful result or the last error.
func RetryFn[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	delay := cfg.Base
	var lastErr error
	for i := 0; i < cfg.Attempts; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if isPermanent(err) {
			return zero, err
		}
		if i < cfg.Attempts-1 {
			abort, next := backoffDelay(ctx, delay, cfg.Max)
			if abort {
				return zero, errors.Join(lastErr, ctx.Err())
			}
			delay = next
		}
	}
	return zero, lastErr
}

// isPermanent 探测 permanent 标记（fallback 耗尽 / 永久错误）并跳过重试，
// 避免与下游自有重试机制叠加放大上游调用。
func isPermanent(err error) bool {
	var perm permanentMarker
	return errors.As(err, &perm) && perm.Permanent()
}

// streamReplayError 标记「已向客户端输出过 token 后失败」的错误：图级重试会
// 再次全量推流，前端得到重复/错乱内容（截断时 token 已逐 token 推给前端）。
// 按永久错误处理，只允许「首 token 尚未输出」的失败重试。
type streamReplayError struct{ err error }

func (e *streamReplayError) Error() string { return e.err.Error() }
func (e *streamReplayError) Unwrap() error { return e.err }

// Permanent 实现 permanentMarker 接口，供 RetryFn 判定不重试。
func (e *streamReplayError) Permanent() bool { return true }

// markStreamReplayPermanent 包装 err 为永久错误（保留 errors.Is/As 语义），
// 禁止 RetryFn 对已输出内容的流重试。
func markStreamReplayPermanent(err error) error {
	if err == nil {
		return nil
	}
	return &streamReplayError{err: err}
}

// backoffDelay 等待 delay 后返回下一次退避时长（倍增，cap 于 max）。
// ctx 取消时返回 abort=true，调用方应立即中止。
func backoffDelay(ctx context.Context, delay, max time.Duration) (abort bool, next time.Duration) {
	select {
	case <-ctx.Done():
		return true, delay
	case <-time.After(delay):
	}
	delay *= 2
	if delay > max {
		delay = max
	}
	return false, delay
}

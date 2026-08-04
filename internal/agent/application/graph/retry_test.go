package graph_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/stretchr/testify/require"
)

func TestRetryFn_SuccessFirstTry(t *testing.T) {
	calls := 0
	result, err := graph.RetryFn(context.Background(), graph.DefaultRetry, func() (string, error) {
		calls++
		return "ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Equal(t, 1, calls)
}

func TestRetryFn_SuccessOnThirdTry(t *testing.T) {
	calls := 0
	cfg := graph.RetryConfig{Attempts: 3, Base: time.Millisecond, Max: 10 * time.Millisecond}
	result, err := graph.RetryFn(context.Background(), cfg, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("transient")
		}
		return 42, nil
	})
	require.NoError(t, err)
	require.Equal(t, 42, result)
	require.Equal(t, 3, calls)
}

func TestRetryFn_AllFail(t *testing.T) {
	calls := 0
	cfg := graph.RetryConfig{Attempts: 3, Base: time.Millisecond, Max: 10 * time.Millisecond}
	_, err := graph.RetryFn(context.Background(), cfg, func() (int, error) {
		calls++
		return 0, errors.New("permanent")
	})
	require.ErrorContains(t, err, "permanent")
	require.Equal(t, 3, calls)
}

func TestRetryFn_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := graph.RetryConfig{Attempts: 3, Base: time.Millisecond, Max: 10 * time.Millisecond}
	_, err := graph.RetryFn(ctx, cfg, func() (int, error) {
		return 0, errors.New("fail")
	})
	require.Error(t, err)
}

// permanentErr 鸭子类型实现下游 permanent 标记（与 llmgateway 的
// permanentError 同构），验证 RetryFn 对 fallback 耗尽/永久错误跳过重试。
type permanentErr struct{ inner error }

func (e *permanentErr) Error() string   { return e.inner.Error() }
func (e *permanentErr) Unwrap() error   { return e.inner }
func (e *permanentErr) Permanent() bool { return true }

// TestRetryFn_SkipsRetryOnPermanent 验证 fallback 链耗尽（permanent 标记）
// 不被 agent 层 RetryFn 放大——重试预算防放大是本设计的核心约束。
func TestRetryFn_SkipsRetryOnPermanent(t *testing.T) {
	calls := 0
	cfg := graph.RetryConfig{Attempts: 3, Base: time.Millisecond, Max: 10 * time.Millisecond}
	_, err := graph.RetryFn(context.Background(), cfg, func() (int, error) {
		calls++
		return 0, &permanentErr{inner: errors.New("fallback chain exhausted")}
	})
	require.Error(t, err)
	require.Equal(t, 1, calls, "permanent 标记错误必须跳过重试，避免与 fallback 循环叠加放大")
	// 包装后仍被识别（errors.As 穿透 %w 链）。
	_, err = graph.RetryFn(context.Background(), cfg, func() (int, error) {
		calls++
		return 0, fmt.Errorf("wrap: %w", &permanentErr{inner: errors.New("exhausted")})
	})
	require.Error(t, err)
	require.Equal(t, 2, calls)
}

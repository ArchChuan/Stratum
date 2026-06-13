package graph_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/graph"
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

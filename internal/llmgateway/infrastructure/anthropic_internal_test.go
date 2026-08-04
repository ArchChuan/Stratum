package infrastructure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newAnthropicTestClient() *AnthropicClient {
	return NewAnthropicClient(ProviderConfig{Name: "test-anthropic", BaseURL: "http://api.test"}, zap.NewNop())
}

func TestClassifyStatus(t *testing.T) {
	client := newAnthropicTestClient()
	cases := []struct {
		name       string
		status     int
		wantRetry  bool
		wantErrMsg string
	}{
		{name: "429 is retryable", status: http.StatusTooManyRequests, wantRetry: true, wantErrMsg: "complete status 429"},
		{name: "500 is retryable", status: http.StatusInternalServerError, wantRetry: true, wantErrMsg: "complete status 500"},
		{name: "400 is not retryable", status: http.StatusBadRequest, wantRetry: false, wantErrMsg: "complete status 400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, retry := client.classifyStatus(tc.status, http.Header{})
			require.Equal(t, tc.wantRetry, retry)
			require.ErrorContains(t, err, tc.wantErrMsg)
		})
	}
}

func TestClassifyStreamStatus(t *testing.T) {
	client := newAnthropicTestClient()
	cases := []struct {
		name       string
		status     int
		wantRetry  bool
		wantErrMsg string
	}{
		{name: "429 is retryable", status: http.StatusTooManyRequests, wantRetry: true, wantErrMsg: "stream status 429"},
		{name: "503 is retryable", status: http.StatusServiceUnavailable, wantRetry: true, wantErrMsg: "stream status 503"},
		{name: "401 is not retryable", status: http.StatusUnauthorized, wantRetry: false, wantErrMsg: "stream status 401"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, retry := client.classifyStreamStatus(tc.status, http.Header{})
			require.Equal(t, tc.wantRetry, retry)
			require.ErrorContains(t, err, tc.wantErrMsg)
		})
	}
}

func TestTruncateBodyPreview(t *testing.T) {
	short := "short body"
	require.Equal(t, short, truncateBodyPreview([]byte(short)))

	long := strings.Repeat("x", 500)
	got := truncateBodyPreview([]byte(long))
	require.Len(t, got, 203)
	require.True(t, strings.HasSuffix(got, "..."))
	require.Equal(t, strings.Repeat("x", 200), got[:200])
}

func TestDrainNonOKStream_retryable(t *testing.T) {
	client := newAnthropicTestClient()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("rate limited")),
	}
	err, retry := client.drainNonOKStream(resp, 0)
	require.ErrorContains(t, err, "stream status 429")
	require.True(t, retry)
}

func TestDrainNonOKStream_nonRetryable(t *testing.T) {
	client := newAnthropicTestClient()
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("bad request")),
	}
	err, retry := client.drainNonOKStream(resp, 2)
	require.ErrorContains(t, err, "stream status 400")
	require.False(t, retry)
}

func TestStreamHTTPErr(t *testing.T) {
	client := newAnthropicTestClient()

	t.Run("non-cancel cause surfaces error", func(t *testing.T) {
		idleCtx, cancel := context.WithCancelCause(context.Background())
		cancel(errors.New("idle timeout"))
		err := client.streamHTTPErr(idleCtx, errors.New("transport error"))
		require.ErrorContains(t, err, "idle timeout")
	})

	t.Run("canceled cause is suppressed", func(t *testing.T) {
		idleCtx, cancel := context.WithCancelCause(context.Background())
		cancel(context.Canceled)
		require.Nil(t, client.streamHTTPErr(idleCtx, context.Canceled))
	})

	t.Run("no cause returns nil", func(t *testing.T) {
		require.Nil(t, client.streamHTTPErr(context.Background(), errors.New("transport error")))
	})
}

package infrastructure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newOllamaTestClient() *OllamaClient {
	return NewOllamaClient(ProviderConfig{Name: "test-ollama", BaseURL: "http://api.test"}, zap.NewNop())
}

func TestOllamaClassifyStatus(t *testing.T) {
	client := newOllamaTestClient()
	cases := []struct {
		name       string
		status     int
		wantRetry  bool
		wantErrMsg string
	}{
		{name: "429 is retryable", status: http.StatusTooManyRequests, wantRetry: true, wantErrMsg: "complete status 429"},
		{name: "500 is retryable", status: http.StatusInternalServerError, wantRetry: true, wantErrMsg: "complete status 500"},
		{name: "400 is not retryable", status: http.StatusBadRequest, wantRetry: false, wantErrMsg: "kind=ollama"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, retry := client.classifyStatus(tc.status, http.Header{})
			require.Equal(t, tc.wantRetry, retry)
			require.ErrorContains(t, err, tc.wantErrMsg)
		})
	}
}

func TestOllamaClassifyStatus_nonRetryableWrapsUpstreamError(t *testing.T) {
	client := newOllamaTestClient()
	err, retry := client.classifyStatus(http.StatusBadRequest, http.Header{})
	require.False(t, retry)
	require.ErrorIs(t, err, domain.ErrUpstreamRequestFailed)
}

func TestOllamaClassifyStreamStatus(t *testing.T) {
	client := newOllamaTestClient()
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

func TestOllamaDrainNonOKStream_retryable(t *testing.T) {
	client := newOllamaTestClient()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("rate limited")),
	}
	err, retry := client.drainNonOKStream(resp, 0)
	require.ErrorContains(t, err, "stream status 429")
	require.True(t, retry)
}

func TestOllamaDrainNonOKStream_nonRetryable(t *testing.T) {
	client := newOllamaTestClient()
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("bad request")),
	}
	err, retry := client.drainNonOKStream(resp, 2)
	require.ErrorContains(t, err, "stream status 400")
	require.False(t, retry)
}

func TestOllamaStreamHTTPErr(t *testing.T) {
	client := newOllamaTestClient()

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

func TestOllamaBatchSize(t *testing.T) {
	client := newOllamaTestClient()
	require.Equal(t, 100, client.BatchSize())

	client.cfg.EmbedBatchSize = 42
	require.Equal(t, 42, client.BatchSize())
}

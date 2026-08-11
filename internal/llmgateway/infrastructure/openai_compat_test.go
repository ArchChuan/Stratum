package infrastructure

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestComplete_400ContextLengthExceeded 验证协议层对 400 响应体的
// context_length_exceeded 语义化：识别为 ErrContextLengthExceeded（永久、
// 不重试，可被 agent 层降级探测）；其他 400 保持普通 status 错误不变。
func TestComplete_400ContextLengthExceeded(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantCLE bool // 期望被识别为上下文超限
	}{
		{
			name:    "error.code is context_length_exceeded",
			body:    `{"error":{"code":"context_length_exceeded","message":"maximum context length reached"}}`,
			wantCLE: true,
		},
		{
			name:    "message contains maximum context length",
			body:    `{"error":{"code":"invalid_request_error","message":"This model's maximum context length is 32768 tokens"}}`,
			wantCLE: true,
		},
		{
			name:    "message contains context_length_exceeded",
			body:    `{"error":{"message":"request exceeds context_length_exceeded"}}`,
			wantCLE: true,
		},
		{
			name:    "other 400 stays plain status error",
			body:    `{"error":{"code":"invalid_request_error","message":"schema mismatch"}}`,
			wantCLE: false,
		},
		{
			name:    "non-JSON 400 body stays plain",
			body:    "plain text error page",
			wantCLE: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			client := NewOpenAICompatClient(ProviderConfig{
				Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
			}, zap.NewNop())
			resp, err := client.Complete(context.Background(),
				&CompletionRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
			require.Nil(t, resp)
			require.Error(t, err)
			if tc.wantCLE {
				require.True(t, IsContextLengthExceeded(err), "err = %v", err)
				require.False(t, isTransient(err), "context length exceeded must be permanent")
			} else {
				require.False(t, IsContextLengthExceeded(err), "err = %v", err)
				require.Contains(t, err.Error(), "status 400")
			}
		})
	}
}

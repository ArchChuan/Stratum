// Package handler — agent_exec_terminal_payload_test.go.
//
// F4 的防漂移守卫（范式同 agent_exec_approval_payload_test.go）：done / error
// 终态帧载荷的真实现在应用层（agent.AgentService.DonePayloadBytes /
// ErrorPayloadBytes），handler 侧只剩一行薄包装。若有人把实现重新内联回 handler，
// 两份真相必然漂移，故这里逐字节断言恒等。
package handler

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// payloadTestHandler 用与生产一致的注入口装配 handler：错误文本的事实源是
// api/middleware 的映射表（生产由 api/wiring 装配），测试照抄同一份保证一致。
func payloadTestHandler(t *testing.T) *AgentHandler {
	t.Helper()
	svc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		PublicErrorMapper: func(err error) (string, string) {
			d := middleware.DescribePublicError(err, middleware.MapErrorToStatus(err))
			return d.Message, d.Code
		},
	})
	return NewAgentHandler(svc, zap.NewNop())
}

// 薄包装与应用层实现必须逐字节相等——防止「两份真相漂移」的编译期外守卫。
func TestAgentExecutionDonePayloadMatchesApplicationLayer(t *testing.T) {
	h := payloadTestHandler(t)
	cases := []struct {
		name   string
		result *domain.AgentResult
	}{
		{name: "minimal", result: &domain.AgentResult{AgentID: "a1", Output: "ok"}},
		{name: "nil slices serialize as empty arrays", result: &domain.AgentResult{
			AgentID: "a1", Output: "ok", Steps: 2, TokensUsed: 7, Duration: 1500 * time.Millisecond,
			Sources: []agentport.RAGSearchSource{{DocumentID: "d1", Snippet: "c", Score: 0.9, HasScore: true}},
		}},
		{name: "task snapshot whitelisted", result: &domain.AgentResult{
			AgentID: "a1", Metadata: map[string]any{"task": map[string]any{"goal": "g"}, "secret": "x"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.agentExecutionDonePayload(tc.result)
			want := h.svc.DonePayloadBytes(tc.result)
			if !bytes.Equal(got, want) {
				t.Fatalf("handler wrapper drifted from application layer:\n got=%s\nwant=%s", got, want)
			}
		})
	}
}

// error 帧同理，且顺带守住安全红线：任何路径都不得回落 err.Error()。
func TestAgentExecutionErrorPayloadMatchesApplicationLayer(t *testing.T) {
	h := payloadTestHandler(t)
	cases := []struct {
		name string
		err  error
	}{
		{name: "mapped domain error", err: fmt.Errorf("assemble options: %w", domain.ErrAssistantModelUnavailable)},
		{name: "unmapped internal error", err: errors.New("provider api_key=do-not-leak")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.agentExecutionErrorPayload(tc.err)
			want := h.svc.ErrorPayloadBytes(tc.err)
			if !bytes.Equal(got, want) {
				t.Fatalf("handler wrapper drifted from application layer:\n got=%s\nwant=%s", got, want)
			}
			if bytes.Contains(got, []byte("api_key=do-not-leak")) {
				t.Fatalf("原始错误文本泄漏进流内 error 帧：%s", got)
			}
		})
	}
}

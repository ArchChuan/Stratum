package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

func TestSemanticallyRelated(t *testing.T) {
	cases := []struct {
		name       string
		goal, text string
		want       bool
	}{
		{"same goal restated", "迁移订单服务到新架构", "继续迁移订单服务", true},
		{"exact", "迁移订单服务", "迁移订单服务", true},
		{"unrelated topic", "迁移订单服务到新架构", "帮我写一首诗", false},
		{"empty text", "迁移订单服务", "", false},
		{"empty goal", "", "迁移订单服务", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticallyRelated(tc.goal, tc.text); got != tc.want {
				t.Fatalf("semanticallyRelated(%q, %q) = %v, want %v", tc.goal, tc.text, got, tc.want)
			}
		})
	}
}

func TestMaybeInjectTaskResume(t *testing.T) {
	conv := "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name         string
		latest       *domain.Task
		latestErr    error
		input        string
		goal         string
		wantInjected bool
	}{
		{
			name: "active task semantically related injects",
			latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
				Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
			input:        "继续迁移订单服务",
			wantInjected: true,
		},
		{
			name: "unrelated message does not inject",
			latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
				Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
			input:        "帮我写一首诗",
			wantInjected: false,
		},
		{
			name: "no next action does not inject",
			latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
				Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: ""},
			input:        "继续迁移订单服务",
			wantInjected: false,
		},
		{
			name: "completed task does not inject",
			latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
				Status: domain.TaskStatusCompleted, Goal: "迁移订单服务", NextAction: "验证迁移"},
			input:        "继续迁移订单服务",
			wantInjected: false,
		},
		{
			name:         "no active task does not inject",
			latest:       nil,
			input:        "继续迁移订单服务",
			wantInjected: false,
		},
		{
			name: "read failure fails closed",
			latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
				Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
			latestErr:    context.DeadlineExceeded,
			input:        "继续迁移订单服务",
			wantInjected: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockTaskRepo{latestActive: tc.latest, latestErr: tc.latestErr}
			agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
			ec := agentExecContext{agentID: "agent-1", cfg: &ExecutionConfig{
				TenantID: "tenant-1", UserID: "user-1", ConversationID: conv}}
			msgs := []port.LLMMessage{{Role: "user", Content: tc.input}}
			ec.input = tc.input
			out := agent.maybeInjectTaskResume(context.Background(), ec, msgs)
			if got := len(out) > len(msgs); got != tc.wantInjected {
				t.Fatalf("injected: got %v want %v (out len %d vs base %d)", got, tc.wantInjected, len(out), len(msgs))
			}
		})
	}
}

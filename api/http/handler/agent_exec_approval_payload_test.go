// Package handler — agent_exec_approval_payload_test.go.
//
// 裁定 8 的防漂移守卫（经人类批准的 brief 外新增）：approval_required 帧载荷的
// 真实现在应用层 agent.ApprovalRequiredPayloadBytes，handler 侧只剩一行薄包装。
// 若有人把实现重新内联回 handler，两份真相必然漂移，故这里逐字节断言恒等，
// 并把 SSE 帧与非流式 202 体的形状绑死。
package handler

import (
	"bytes"
	"encoding/json"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

func approvalPayloadCases() []struct {
	name      string
	approvals []agentport.ToolApprovalRequiredError
} {
	first := agentport.ToolApprovalRequiredError{
		ApprovalID: "ap-1", ToolCallID: "tc-1", ServerID: "srv-1",
		ToolName: "delete_workspace", RiskLevel: domain.ToolRiskDestructive,
	}
	second := agentport.ToolApprovalRequiredError{
		ApprovalID: "ap-2", ToolCallID: "tc-2", ServerID: "srv-2",
		ToolName: "drop_table", RiskLevel: domain.ToolRiskWriteReversible,
	}
	return []struct {
		name      string
		approvals []agentport.ToolApprovalRequiredError
	}{
		{name: "empty", approvals: nil},
		{name: "single", approvals: []agentport.ToolApprovalRequiredError{first}},
		{name: "batch", approvals: []agentport.ToolApprovalRequiredError{first, second}},
	}
}

// 薄包装与应用层实现必须逐字节相等——这是防止「两份真相漂移」的编译期外守卫。
func TestApprovalRequiredSSEPayloadMatchesApplicationLayer(t *testing.T) {
	for _, tc := range approvalPayloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := approvalRequiredSSEPayload(tc.approvals)
			want := agentapp.ApprovalRequiredPayloadBytes(tc.approvals)
			if !bytes.Equal(got, want) {
				t.Fatalf("handler wrapper drifted from application layer:\n got=%s\nwant=%s", got, want)
			}
		})
	}
}

// SSE 帧与非流式 202 体是同一条 wire 契约的两种传输。两者同为 map 序列化
// （json.Marshal 按键排序），因此可以直接逐字节比对；一旦有人只改其中一侧的
// 键名或镜像逻辑，这里立刻变红。
func TestApprovalRequiredSSEPayloadMatchesAcceptedResponse(t *testing.T) {
	for _, tc := range approvalPayloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			frame := approvalRequiredSSEPayload(tc.approvals)
			accepted, err := json.Marshal(approvalAcceptedResponse(tc.approvals))
			if err != nil {
				t.Fatalf("marshal accepted response: %v", err)
			}
			if !bytes.Equal(frame, accepted) {
				t.Fatalf("SSE frame diverged from 202 body:\nframe=%s\n  202=%s", frame, accepted)
			}
		})
	}
}

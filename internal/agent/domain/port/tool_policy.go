package port

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type ToolRiskLevel = domain.ToolRiskLevel

const (
	ToolRiskRead            = domain.ToolRiskRead
	ToolRiskWriteReversible = domain.ToolRiskWriteReversible
	ToolRiskDestructive     = domain.ToolRiskDestructive
	ToolRiskUnclassified    = domain.ToolRiskUnclassified
)

func ParseToolRiskLevel(value any) ToolRiskLevel {
	level, _ := value.(string)
	switch ToolRiskLevel(level) {
	case ToolRiskRead, ToolRiskWriteReversible, ToolRiskDestructive:
		return ToolRiskLevel(level)
	default:
		return ToolRiskUnclassified
	}
}

type ToolApprovalRequest struct {
	TenantID, TraceID, ExecutionID, ToolCallID string
	ServerID, ToolName                         string
	RiskLevel                                  ToolRiskLevel
	Arguments                                  map[string]any
}

type ToolApprovalRequester func(context.Context, ToolApprovalRequest) (string, error)

type ToolApprovalRequiredError struct {
	ApprovalID, ToolCallID, ServerID, ToolName string
	RiskLevel                                  ToolRiskLevel
}

func (e *ToolApprovalRequiredError) Error() string {
	return fmt.Sprintf("tool approval required: approval=%s tool=%s risk=%s", e.ApprovalID, e.ToolName, e.RiskLevel)
}

// BatchToolApprovalRequiredError 表示同一轮 LLM 消息有多个工具调用需要审批。
// 整轮暂停：所有需审批调用一次性创建审批，工具一个都不执行，等待用户全部处理。
type BatchToolApprovalRequiredError struct {
	Errors []ToolApprovalRequiredError
}

func (e *BatchToolApprovalRequiredError) Error() string {
	return fmt.Sprintf("tool approval required: %d pending approvals", len(e.Errors))
}

type MCPToolPolicyResolver interface {
	ResolveMCPToolRisk(ctx context.Context, tenantID, serverID, toolName string) (ToolRiskLevel, error)
}

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
type ApprovedToolCallFn func(context.Context, string, string, map[string]any) (output any, handled bool, err error)

type ToolApprovalRequiredError struct {
	ApprovalID, ToolCallID, ServerID, ToolName string
	RiskLevel                                  ToolRiskLevel
}

func (e *ToolApprovalRequiredError) Error() string {
	return fmt.Sprintf("tool approval required: approval=%s tool=%s risk=%s", e.ApprovalID, e.ToolName, e.RiskLevel)
}

type MCPToolPolicyResolver interface {
	ResolveMCPToolRisk(ctx context.Context, tenantID, serverID, toolName string) (ToolRiskLevel, error)
}

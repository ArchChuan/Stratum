// Test-only exports — visible to external test packages
// (application_test) but not compiled into the production binary.

package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// BuildExtraToolsForTest exposes buildExtraTools to external test packages.
func (s *AgentService) BuildExtraToolsForTest(ctx context.Context, tenantID string, mcpServerIDs, allowedSkills []string) ([]port.ToolDefinition, map[string]port.SkillToolRef) {
	return s.buildExtraTools(ctx, tenantID, "test-subject", mcpServerIDs, allowedSkills)
}

// RecordExecutionForTest exposes recordExecution to external test packages.
func (s *AgentService) RecordExecutionForTest(reqCtx context.Context, executionID, id, userID, agentName, query string, result *AgentResult, err error, durationMs int) {
	s.recordExecution(reqCtx, executionID, id, userID, agentName, query, result, err, durationMs)
}

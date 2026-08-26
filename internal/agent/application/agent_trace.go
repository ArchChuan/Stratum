// Tool-call trace listing.

package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/google/uuid"
)

func (s *AgentService) ListToolTraces(
	ctx context.Context, tenantID, userID, traceID string,
) ([]ToolObservation, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	if err := s.authorizeTraceOwner(ctx, tenantID, userID, traceID); err != nil {
		return nil, err
	}
	return s.deps.EvidenceProvider.ToolObservations(ctx, tenantID, traceID)
}

func (s *AgentService) ListTraceEvents(
	ctx context.Context, tenantID, userID, traceID string,
) ([]AgentTraceEvent, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	if err := s.authorizeTraceOwner(ctx, tenantID, userID, traceID); err != nil {
		return nil, err
	}
	return s.deps.EvidenceProvider.TraceEvents(ctx, tenantID, traceID)
}

func (s *AgentService) authorizeTraceOwner(ctx context.Context, tenantID, userID, traceID string) error {
	evidence, err := s.deps.EvidenceProvider.Resolve(ctx, tenantID, traceID)
	if err != nil {
		return err
	}
	if userID == "" || evidence.UserID != userID {
		return domain.ErrEvidenceNotFound
	}
	return nil
}

// executionIDOrNew returns id if non-empty, otherwise generates a new v7 UUID.

func executionIDOrNew(id string) string {
	if id == "" {
		return uuid.Must(uuid.NewV7()).String()
	}
	return id
}

// resolveExecutionWindow 执行时解析 agent 窗口（Spec 第 1 节两阶段），
// 替代 Create/Update 的一次性固化：管理员后补配置下次执行立即生效。
// 返回 (解析窗口, 来源)；模型窗口来源为 vendor_table/fallback 时 WARN。

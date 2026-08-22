package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// ReflectAndPersist 处理一条工具轨迹反思任务：解析骨架 → 触发 gate →
// 反思模型提炼 → 证据门/质量门 → 复用事实持久化链入库。
// 原始 tool steps 不在任何环节入库。
func (s *MemoryService) ReflectAndPersist(ctx context.Context, task *port.ReflectionTask) error {
	if task == nil {
		return fmt.Errorf("memory.reflection: task is nil")
	}
	s.logger.Debug("memory.reflection.start",
		zap.String("tenant_id", task.TenantID),
		zap.String("user_id", task.UserID),
		zap.String("agent_id", task.AgentID),
		zap.String("execution_id", task.ExecutionID),
	)

	skeleton, err := parseReflectionSkeleton(task)
	if err != nil {
		return err
	}

	// 触发 gate（确定性规则）：低价值任务直接 ack，不调用 LLM、不产生写入。
	if !skeleton.ShouldReflect(constants.MemoryReflectionMinToolCalls, task.ExplicitMemory) {
		s.logger.Debug("memory.reflection.skip",
			zap.String("execution_id", skeleton.ExecutionID),
			zap.Int("steps", len(skeleton.Steps)),
		)
		return nil
	}

	reflector, err := s.resolveReflector(ctx, task.TenantID)
	if err != nil {
		return err
	}

	entries, err := reflector.Reflect(ctx, task.TenantID, skeleton, "")
	if err != nil {
		return fmt.Errorf("memory.reflection: reflect: %w", err)
	}
	if len(entries) == 0 {
		s.logger.Debug("memory.reflection.no_entries", zap.String("execution_id", skeleton.ExecutionID))
		return nil
	}

	// 证据门：反思条目必须携带 execution_id，否则视为模型幻觉丢弃。
	valid := filterReflectionEntries(skeleton, entries)
	if len(valid) == 0 {
		s.logger.Debug("memory.reflection.no_valid_entries", zap.String("execution_id", skeleton.ExecutionID))
		return nil
	}
	if len(valid) > constants.MemoryReflectionMaxEntries {
		valid = valid[:constants.MemoryReflectionMaxEntries]
	}

	req := &ExtractFactsRequest{
		TenantID:        task.TenantID,
		UserID:          task.UserID,
		AgentID:         task.AgentID,
		ConversationID:  task.ConversationID,
		Scope:           task.Scope,
		SourceMessageID: skeleton.ExecutionID,
	}
	return s.persistExtractedFacts(ctx, req, reflectionEntriesToIndexed(valid), domain.FactSourceTrajectoryReflection)
}

// parseReflectionSkeleton 解析并校验任务骨架；错误向上传播（worker 走 DLQ）。
func parseReflectionSkeleton(task *port.ReflectionTask) (domain.TrajectorySkeleton, error) {
	var skeleton domain.TrajectorySkeleton
	if err := json.Unmarshal(task.Skeleton, &skeleton); err != nil {
		return skeleton, fmt.Errorf("memory.reflection: unmarshal skeleton: %w", err)
	}
	if err := skeleton.Validate(); err != nil {
		return skeleton, fmt.Errorf("memory.reflection: invalid skeleton: %w", err)
	}
	return skeleton, nil
}

// resolveReflector 解析当前租户的反思模型；不可用时返回错误（fail-closed）。
func (s *MemoryService) resolveReflector(ctx context.Context, tenantID string) (port.TrajectoryReflector, error) {
	r := s.reflector
	if r == nil && s.reflectorResolver != nil {
		r = s.reflectorResolver(ctx, tenantID)
	}
	if r == nil {
		return nil, fmt.Errorf("memory.reflection: reflector not available for tenant %s", tenantID)
	}
	return r, nil
}

// filterReflectionEntries 证据门：只保留携带当前 execution_id 的条目；
// 缺失或指向其他执行的条目视为模型幻觉丢弃。
func filterReflectionEntries(skeleton domain.TrajectorySkeleton, entries []*port.ReflectionEntry) []*port.ReflectionEntry {
	valid := make([]*port.ReflectionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Evidence.ExecutionID == "" {
			continue
		}
		if entry.Evidence.ExecutionID != skeleton.ExecutionID {
			continue
		}
		valid = append(valid, entry)
	}
	return valid
}

// reflectionEntriesToIndexed 把反思条目转换为提取事实索引（ordinal 稳定）。
func reflectionEntriesToIndexed(entries []*port.ReflectionEntry) []indexedExtractedFact {
	indexed := make([]indexedExtractedFact, 0, len(entries))
	for i, entry := range entries {
		indexed = append(indexed, indexedExtractedFact{
			Fact:            entry.ToExtractedFact(),
			OriginalOrdinal: i,
		})
	}
	return indexed
}

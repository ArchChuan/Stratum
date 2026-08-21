package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// effectiveConfidence returns the extraction confidence used for quality sorting and filtering.
// If ef.Confidence is explicitly set (non-nil), that value is used.
// If omitted (nil), the Importance field serves as a proxy (same semantics as before Phase 0).
func effectiveConfidence(ef *port.ExtractedFact) float64 {
	if ef.Confidence != nil {
		return *ef.Confidence
	}
	return ef.Importance
}

// ExtractFacts orchestrates fact extraction: LLM extraction → low-confidence gate
// → quality sort → per-round cap → supersede check → entity normalization → persistence.
func (s *MemoryService) ExtractFacts(ctx context.Context, req *ExtractFactsRequest) error {
	s.logger.Debug("memory.extract_facts",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
		zap.String("agent_id", req.AgentID),
		zap.String("conversation_id", req.ConversationID),
		zap.Int("message_count", len(req.Messages)),
	)

	var fullContent string
	for _, msg := range req.Messages {
		fullContent += msg.Role + ": " + msg.Content + "\n"
	}

	extractor := s.llmExtract
	if extractor == nil && s.llmExtractResolver != nil {
		extractor = s.llmExtractResolver(ctx, req.TenantID)
	}
	if extractor == nil {
		return fmt.Errorf("llm extractor not available for tenant %s", req.TenantID)
	}
	extractedFacts, err := extractor.ExtractFacts(ctx, req.UserID, req.AgentID, fullContent)
	if err != nil {
		s.logger.Error("memory.extract_facts: llm extraction failed",
			zap.String("tenant_id", req.TenantID),
			zap.String("user_id", req.UserID),
			zap.Error(err),
		)
		return fmt.Errorf("llm extract: %w", err)
	}
	s.logger.Info("memory.extract_facts: llm extracted",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
		zap.Int("fact_count", len(extractedFacts)),
	)

	// Assign ordinals before quality gates so retries keep the LLM output identity.
	indexedFacts := indexedExtractedFacts(extractedFacts)
	for _, item := range indexedFacts {
		ef := item.Fact
		ec := effectiveConfidence(ef)
		if ec < constants.FactConfidenceMin {
			s.logger.Debug("memory.extract_facts: dropped low-confidence fact",
				zap.String("content_prefix", truncateStr(ef.Content, 40)),
				zap.Float64("effective_confidence", ec),
				zap.Float64("threshold", constants.FactConfidenceMin),
			)
		}
	}
	beforeLimit := len(indexedFacts)
	indexedFacts = qualityFilterAndSortExtractedFacts(indexedFacts, constants.FactPerRoundPersistLimit)
	if beforeLimit > constants.FactPerRoundPersistLimit && len(indexedFacts) == constants.FactPerRoundPersistLimit {
		s.logger.Debug("memory.extract_facts: capped per-round persist limit",
			zap.Int("limit", constants.FactPerRoundPersistLimit),
		)
	}

	return s.persistExtractedFacts(ctx, req, indexedFacts, domain.FactSourceLLMExtraction)
}

// persistExtractedFacts 是 ExtractFacts 与轨迹反思共享的持久化链：
// supersede 冲突消解 → entity 归一化 → 事实写入 → 向量入库。
// source 区分来源（llm_extraction / trajectory_reflection），provenance
// 由 req 的 source 身份（message_id/task_id/ordinal）承载。
func (s *MemoryService) persistExtractedFacts(
	ctx context.Context,
	req *ExtractFactsRequest,
	indexedFacts []indexedExtractedFact,
	source string,
) error {
	for _, indexedFact := range indexedFacts {
		if err := s.persistExtractedFact(ctx, req, indexedFact, source); err != nil {
			return err
		}
	}

	s.logger.Info("memory.extract_facts: done",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
		zap.Int("facts_stored", len(indexedFacts)),
	)
	return nil
}

// persistExtractedFact 持久化单条提取/反思事实：canonical 身份 → supersede
// 候选 → 事实写入 → 冲突消解 → 向量入库。
func (s *MemoryService) persistExtractedFact(
	ctx context.Context,
	req *ExtractFactsRequest,
	indexedFact indexedExtractedFact,
	source string,
) error {
	extractedFact := indexedFact.Fact
	canonical, err := s.resolveCanonicalFact(req, extractedFact, indexedFact.OriginalOrdinal)
	if err != nil {
		return err
	}
	candidates, err := s.supersedeCandidatesFor(ctx, req, extractedFact.Content)
	if err != nil {
		return err
	}
	fact, err := s.newFactFromExtracted(req, extractedFact, source)
	if err != nil {
		return err
	}
	attemptedFactID := fact.ID
	written, created, err := s.writeExtractedFact(ctx, req, fact, canonical, extractedFact.Entities)
	if err != nil {
		s.logger.Error("memory.extract_facts: persist fact failed",
			zap.String("tenant_id", req.TenantID),
			zap.String("fact_id", attemptedFactID),
			zap.Error(err),
		)
		return fmt.Errorf("insert fact: %w", err)
	}
	s.applySupersedeJudgments(ctx, req, candidates, written, created)
	return s.embedAndStoreFactVector(ctx, req, written)
}

// resolveCanonicalFact 在存在稳定 source 身份时计算 canonical 事实；否则返回
// nil（走普通创建路径）。
func (s *MemoryService) resolveCanonicalFact(
	req *ExtractFactsRequest,
	extractedFact *port.ExtractedFact,
	ordinal int,
) (*canonicalExtractedFact, error) {
	if req.SourceMessageID == "" && req.SourceTaskID == 0 {
		return nil, nil
	}
	canonical, err := canonicalizeExtractedFact(req, extractedFact, ordinal)
	if err != nil {
		return nil, fmt.Errorf("canonicalize extracted fact: %w", err)
	}
	extractedFact.Entities = canonical.Entities
	return canonical, nil
}

// supersedeCandidatesFor 按 scope 查询与新内容冲突的既有事实候选。
func (s *MemoryService) supersedeCandidatesFor(ctx context.Context, req *ExtractFactsRequest, content string) ([]*port.SupersedeCandidate, error) {
	writeFilter := domain.ScopeFilter{TenantID: req.TenantID, UserID: req.UserID, AgentID: req.AgentID}
	if req.Scope == string(domain.ScopeAgent) {
		writeFilter.IncludeAgentScope = true
	} else {
		writeFilter.IncludeUserScope = true
	}
	return s.factRepo.FindSupersedeCandidates(
		ctx,
		req.TenantID,
		writeFilter,
		content,
		constants.MemorySupersedeCandidateMin,
		float64(constants.MemorySupersedeCandidateMax),
	)
}

// newFactFromExtracted 构造带 category/confidence/source 的事实实体。
func (s *MemoryService) newFactFromExtracted(
	req *ExtractFactsRequest,
	extractedFact *port.ExtractedFact,
	source string,
) (*domain.MemoryFact, error) {
	category := domain.FactTypeToCategory(extractedFact.FactType)
	confidence := effectiveConfidence(extractedFact)
	fact, err := domain.NewFactWithMeta(
		req.TenantID,
		req.UserID,
		req.AgentID,
		req.ConversationID,
		req.Scope,
		extractedFact.Content,
		extractedFact.Importance,
		confidence,
		category,
		source,
		extractedFact.Entities,
	)
	if err != nil {
		return nil, fmt.Errorf("new fact: %w", err)
	}
	return fact, nil
}

// writeExtractedFact 写入事实行：canonical 身份走幂等 writer，否则普通创建
// 并归一化实体。返回写入后的事实（CreateExtracted 可能返回既有行）。
func (s *MemoryService) writeExtractedFact(
	ctx context.Context,
	req *ExtractFactsRequest,
	fact *domain.MemoryFact,
	canonical *canonicalExtractedFact,
	entityNames []string,
) (*domain.MemoryFact, bool, error) {
	if canonical != nil {
		writer, ok := s.factRepo.(port.ExtractedFactWriter)
		if !ok {
			return nil, false, fmt.Errorf("atomic extracted fact writer not available: %w", domain.ErrInvalidFactSourceIdentity)
		}
		return writer.CreateExtracted(ctx, req.TenantID, &port.ExtractedFactWrite{
			Fact: fact, Identity: canonical.Identity, PayloadHash: canonical.PayloadHash, EntityNames: canonical.Entities,
		})
	}
	for _, entityName := range entityNames {
		if _, err := s.normalizeEntity(ctx, req.TenantID, req.UserID, req.AgentID, req.Scope, entityName); err != nil {
			return nil, false, fmt.Errorf("normalize entity %q: %w", entityName, err)
		}
	}
	return fact, true, s.factRepo.Create(ctx, req.TenantID, fact)
}

// applySupersedeJudgments 对新写入事实做冲突消解：高相似直接 supersede，
// 中相似交给 LLM 判定（每事实最多 3 次）。
func (s *MemoryService) applySupersedeJudgments(
	ctx context.Context,
	req *ExtractFactsRequest,
	candidates []*port.SupersedeCandidate,
	fact *domain.MemoryFact,
	created bool,
) {
	if !created {
		return
	}
	judge := s.judge
	if judge == nil && s.judgeResolver != nil {
		judge = s.judgeResolver(ctx, req.TenantID)
	}
	llmCalls := 0
	for _, candidate := range candidates {
		if candidate.Fact.ID == fact.ID {
			continue
		}
		if candidate.Similarity >= constants.MemoryInlineSupersedeFastThresh {
			s.supersedeCandidate(ctx, req.TenantID, candidate, fact.ID)
			continue
		}
		llmCalls = judgeSupersede(ctx, s, judge, req.TenantID, candidate, fact, llmCalls)
	}
}

// judgeSupersede 执行单次 LLM 冲突判定（每事实最多 MemoryInlineSupersedeLLMPerFact
// 次），返回更新后的调用计数。judge 为空时保持原计数。
func judgeSupersede(
	ctx context.Context,
	svc *MemoryService,
	judge port.LLMSuperseder,
	tenantID string,
	candidate *port.SupersedeCandidate,
	fact *domain.MemoryFact,
	llmCalls int,
) int {
	if judge != nil && llmCalls < constants.MemoryInlineSupersedeLLMPerFact {
		judgment, jerr := judge.JudgeSupersede(ctx, candidate.Fact.Content, fact.Content)
		llmCalls++
		if jerr == nil && judgment.Supersedes {
			svc.supersedeCandidate(ctx, tenantID, candidate, fact.ID)
		}
	}
	return llmCalls
}

// embedAndStoreFactVector 为事实生成向量并写入 Milvus collection。
func (s *MemoryService) embedAndStoreFactVector(ctx context.Context, req *ExtractFactsRequest, fact *domain.MemoryFact) error {
	embedder := s.embedClient
	if embedder == nil && s.embedClientResolver != nil {
		embedder = s.embedClientResolver(ctx, req.TenantID)
	}
	if embedder == nil {
		return fmt.Errorf("embed client not available for tenant %s", req.TenantID)
	}
	vector, err := embedder.Embed(ctx, fact.Content)
	if err != nil {
		return fmt.Errorf("embed text: %w", err)
	}
	collectionName := factsCollectionName(req.TenantID, embedder.Model())
	// vector metadata 包含 category/confidence/source，不含敏感原文以外的新增字段
	doc := &port.VectorDoc{
		ID:        fact.ID,
		Embedding: vector,
		Metadata: map[string]interface{}{
			"user_id":         fact.UserID,
			"agent_id":        fact.AgentID,
			"conversation_id": fact.ConversationID,
			"scope":           string(fact.Scope),
			"content":         fact.Content,
			"importance":      fact.Importance,
			"category":        fact.Category,
			"confidence":      fact.Confidence,
			"source":          fact.Source,
		},
	}
	if err := s.vectorStore.Upsert(ctx, collectionName, []*port.VectorDoc{doc}); err != nil {
		return fmt.Errorf("upsert vector: %w", err)
	}
	return nil
}

// supersedeCandidate marks a candidate superseded, persists the status, and
// deletes its vector so stale content stops being recalled. Returns true only
// when the PG status update succeeded. Vector deletion is best-effort with
// ERROR surfacing: the daily GC purge backstops missed deletions once the row
// passes retention, and recall filters non-active facts regardless.
func (s *MemoryService) supersedeCandidate(ctx context.Context, tenantID string, candidate *port.SupersedeCandidate, newFactID string) bool {
	if err := candidate.Fact.MarkSuperseded(newFactID); err != nil {
		return false
	}
	if err := s.factRepo.Update(ctx, tenantID, candidate.Fact); err != nil {
		s.logger.Error("memory.extract_facts: supersede update failed",
			zap.String("tenant_id", tenantID),
			zap.String("fact_id", candidate.Fact.ID),
			zap.Error(err))
		return false
	}
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteFactVectors(ctx, tenantID, []string{candidate.Fact.ID}); err != nil {
			s.logger.Error("memory.extract_facts: supersede vector delete failed",
				zap.String("tenant_id", tenantID),
				zap.String("fact_id", candidate.Fact.ID),
				zap.Error(err))
		}
	}
	return true
}

// normalizeEntity finds or creates an entity, returning its ID.
// Uses fuzzy matching (trigram similarity) to avoid duplicates.
func (s *MemoryService) normalizeEntity(ctx context.Context, tenantID, userID, agentID, scope, name string) (string, error) {
	filter := domain.ScopeFilter{TenantID: tenantID, UserID: userID, AgentID: agentID}
	if scope == string(domain.ScopeAgent) {
		filter.IncludeAgentScope = true
	} else {
		filter.IncludeUserScope = true
	}
	existing, err := s.entityRepo.FindByNameAndType(ctx, tenantID, filter, name, "", constants.MemorySupersedeCandidateMin)
	if err != nil && err != domain.ErrEntityNotFound {
		return "", fmt.Errorf("find entity: %w", err)
	}

	if existing != nil {
		existing.IncrementFactCount()
		if err := s.entityRepo.Update(ctx, tenantID, existing); err != nil {
			return "", fmt.Errorf("update entity: %w", err)
		}
		return existing.ID, nil
	}

	entity, err := domain.NewEntity(userID, agentID, scope, name, "")
	if err != nil {
		return "", fmt.Errorf("new entity: %w", err)
	}

	if err := s.entityRepo.Create(ctx, tenantID, entity); err != nil {
		return "", fmt.Errorf("insert entity: %w", err)
	}

	return entity.ID, nil
}

// truncateStr returns the first n runes of s (used in log messages to avoid bloat).
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

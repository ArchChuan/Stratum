package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

	// Phase 0: 低置信门控 — 丢弃 effectiveConfidence < FactConfidenceMin 的事实
	filtered := extractedFacts[:0]
	for _, ef := range extractedFacts {
		ec := effectiveConfidence(ef)
		if ec < constants.FactConfidenceMin {
			s.logger.Debug("memory.extract_facts: dropped low-confidence fact",
				zap.String("content_prefix", truncateStr(ef.Content, 40)),
				zap.Float64("effective_confidence", ec),
				zap.Float64("threshold", constants.FactConfidenceMin),
			)
			continue
		}
		filtered = append(filtered, ef)
	}
	extractedFacts = filtered

	// Phase 0: 质量排序 (effectiveConfidence DESC, Importance DESC) 后截断至 FactPerRoundPersistLimit
	if len(extractedFacts) > constants.FactPerRoundPersistLimit {
		sort.SliceStable(extractedFacts, func(i, j int) bool {
			ci := effectiveConfidence(extractedFacts[i])
			cj := effectiveConfidence(extractedFacts[j])
			if ci != cj {
				return ci > cj
			}
			return extractedFacts[i].Importance > extractedFacts[j].Importance
		})
		extractedFacts = extractedFacts[:constants.FactPerRoundPersistLimit]
		s.logger.Debug("memory.extract_facts: capped per-round persist limit",
			zap.Int("limit", constants.FactPerRoundPersistLimit),
		)
	}

	for _, extractedFact := range extractedFacts {
		writeFilter := domain.ScopeFilter{TenantID: req.TenantID, UserID: req.UserID, AgentID: req.AgentID}
		if req.Scope == string(domain.ScopeAgent) {
			writeFilter.IncludeAgentScope = true
		} else {
			writeFilter.IncludeUserScope = true
		}
		candidates, err := s.factRepo.FindSupersedeCandidates(
			ctx,
			req.TenantID,
			writeFilter,
			extractedFact.Content,
			constants.MemorySupersedeCandidateMin,
			float64(constants.MemorySupersedeCandidateMax),
		)
		if err != nil {
			return fmt.Errorf("find supersede candidates: %w", err)
		}

		for _, entityName := range extractedFact.Entities {
			_, err := s.normalizeEntity(ctx, req.TenantID, req.UserID, req.AgentID, req.Scope, entityName)
			if err != nil {
				return fmt.Errorf("normalize entity %q: %w", entityName, err)
			}
		}

		// Phase 0: 构造带 category/confidence/source 的事实
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
			domain.FactSourceLLMExtraction,
			extractedFact.Entities,
		)
		if err != nil {
			return fmt.Errorf("new fact: %w", err)
		}

		if err := s.factRepo.Create(ctx, req.TenantID, fact); err != nil {
			s.logger.Error("memory.extract_facts: persist fact failed",
				zap.String("tenant_id", req.TenantID),
				zap.String("fact_id", fact.ID),
				zap.Error(err),
			)
			return fmt.Errorf("insert fact: %w", err)
		}

		// Inline supersede: high-similarity → direct; mid-range → LLM (max 3/fact).
		llmCallsThisFact := 0
		for _, candidate := range candidates {
			if candidate.Fact.ID == fact.ID {
				continue
			}
			if candidate.Similarity >= constants.MemoryInlineSupersedeFastThresh {
				if merr := candidate.Fact.MarkSuperseded(fact.ID); merr == nil {
					_ = s.factRepo.Update(ctx, req.TenantID, candidate.Fact)
				}
				continue
			}
			if s.judge != nil && llmCallsThisFact < constants.MemoryInlineSupersedeLLMPerFact {
				judgment, jerr := s.judge.JudgeSupersede(ctx, candidate.Fact.Content, fact.Content)
				llmCallsThisFact++
				if jerr == nil && judgment.Supersedes {
					if merr := candidate.Fact.MarkSuperseded(fact.ID); merr == nil {
						_ = s.factRepo.Update(ctx, req.TenantID, candidate.Fact)
					}
				}
			}
		}

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

		collectionName := fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(req.TenantID, "-", "_"))
		// Phase 0: vector metadata 包含 category/confidence/source，不含敏感原文以外的新增字段
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
	}

	s.logger.Info("memory.extract_facts: done",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
		zap.Int("facts_stored", len(extractedFacts)),
	)
	return nil
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

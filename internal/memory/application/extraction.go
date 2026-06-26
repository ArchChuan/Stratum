package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// ExtractFacts orchestrates fact extraction: LLM extraction → supersede check → entity normalization → persistence.
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

	for _, extractedFact := range extractedFacts {
		candidates, err := s.factRepo.FindSupersedeCandidates(
			ctx,
			req.TenantID,
			req.UserID,
			req.AgentID,
			extractedFact.Content,
			constants.MemorySupersedeCandidateMin,
			float64(constants.MemorySupersedeCandidateMax),
		)
		if err != nil {
			return fmt.Errorf("find supersede candidates: %w", err)
		}
		// TODO: LLM supersede judgment (Task 3.5 - deferred)
		_ = candidates

		for _, entityName := range extractedFact.Entities {
			_, err := s.normalizeEntity(ctx, req.TenantID, req.UserID, req.AgentID, entityName)
			if err != nil {
				return fmt.Errorf("normalize entity %q: %w", entityName, err)
			}
		}

		fact, err := domain.NewFact(
			req.TenantID,
			req.UserID,
			req.AgentID,
			req.ConversationID,
			req.Scope,
			extractedFact.Content,
			extractedFact.Importance,
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
		doc := &port.VectorDoc{
			ID:        fact.ID,
			Embedding: vector,
			Metadata: map[string]interface{}{
				"user_id":    fact.UserID,
				"agent_id":   fact.AgentID,
				"content":    fact.Content,
				"importance": fact.Importance,
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
func (s *MemoryService) normalizeEntity(ctx context.Context, tenantID, userID, agentID, name string) (string, error) {
	existing, err := s.entityRepo.FindByNameAndType(ctx, tenantID, userID, name, "", constants.MemorySupersedeCandidateMin)
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

	entity, err := domain.NewEntity(userID, agentID, string(domain.ScopeUser), name, "")
	if err != nil {
		return "", fmt.Errorf("new entity: %w", err)
	}

	if err := s.entityRepo.Create(ctx, tenantID, entity); err != nil {
		return "", fmt.Errorf("insert entity: %w", err)
	}

	return entity.ID, nil
}

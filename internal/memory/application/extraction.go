package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// ExtractFacts orchestrates fact extraction: LLM extraction → supersede check → entity normalization → persistence.
func (s *MemoryService) ExtractFacts(ctx context.Context, req *ExtractFactsRequest) error {
	// Concatenate messages for LLM extraction
	var fullContent string
	for _, msg := range req.Messages {
		fullContent += msg.Role + ": " + msg.Content + "\n"
	}

	// Step 1: Call LLM to extract facts
	extractedFacts, err := s.llmExtract.ExtractFacts(ctx, req.UserID, req.AgentID, fullContent)
	if err != nil {
		return fmt.Errorf("llm extract: %w", err)
	}

	// Step 2: Process each extracted fact
	for _, extractedFact := range extractedFacts {
		// Check supersede candidates (trigram similarity > 0.6)
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
		// For now, skip supersede logic
		_ = candidates

		// Step 3: Normalize entities (upsert via fuzzy match)
		for _, entityName := range extractedFact.Entities {
			_, err := s.normalizeEntity(ctx, req.UserID, req.AgentID, entityName)
			if err != nil {
				return fmt.Errorf("normalize entity %q: %w", entityName, err)
			}
		}

		// Step 4: Create fact domain object
		fact, err := domain.NewFact(
			req.TenantID,
			req.UserID,
			req.AgentID,
			string(domain.ScopeUser),
			extractedFact.Content,
			extractedFact.Importance,
			extractedFact.Entities,
		)
		if err != nil {
			return fmt.Errorf("new fact: %w", err)
		}

		// Step 5: Insert fact to DB
		if err := s.factRepo.Create(ctx, req.TenantID, fact); err != nil {
			return fmt.Errorf("insert fact: %w", err)
		}

		// Step 6: Generate embedding and upsert to Milvus
		vector, err := s.embedClient.Embed(ctx, fact.Content)
		if err != nil {
			return fmt.Errorf("embed text: %w", err)
		}

		collectionName := fmt.Sprintf("memory_facts_%s", req.TenantID)
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

	return nil
}

// normalizeEntity finds or creates an entity, returning its ID.
// Uses fuzzy matching (trigram similarity) to avoid duplicates.
func (s *MemoryService) normalizeEntity(ctx context.Context, userID, agentID, name string) (string, error) {
	// Fuzzy match existing entities (threshold 0.6)
	existing, err := s.entityRepo.FindByNameAndType(ctx, userID, name, "", constants.MemorySupersedeCandidateMin)
	if err != nil && err != domain.ErrEntityNotFound {
		return "", fmt.Errorf("find entity: %w", err)
	}

	if existing != nil {
		// Update fact count and last_seen_at
		existing.IncrementFactCount()
		if err := s.entityRepo.Update(ctx, existing); err != nil {
			return "", fmt.Errorf("update entity: %w", err)
		}
		return existing.ID, nil
	}

	// Create new entity (type inferred as empty for now)
	entity, err := domain.NewEntity(userID, agentID, string(domain.ScopeUser), name, "")
	if err != nil {
		return "", fmt.Errorf("new entity: %w", err)
	}

	if err := s.entityRepo.Create(ctx, entity); err != nil {
		return "", fmt.Errorf("insert entity: %w", err)
	}

	return entity.ID, nil
}

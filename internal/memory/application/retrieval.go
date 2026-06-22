package application

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type scoredFact struct {
	fact  *domain.MemoryFact
	score float64
}

// RecallMemory performs hybrid retrieval: vector search + trigram search + RRF fusion.
func (s *MemoryService) RecallMemory(ctx context.Context, req *RecallMemoryRequest) (*RecallMemoryResponse, error) {
	// Step 1: Embed query for vector search
	queryVector, err := s.embedClient.Embed(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Step 2: Vector search (retrieve 2*topK candidates)
	collectionName := fmt.Sprintf("memory_facts_%s", req.TenantID)
	filter := map[string]interface{}{
		"user_id": req.UserID,
	}

	vectorDocs, err := s.vectorStore.Search(ctx, collectionName, queryVector, req.TopK*2, filter)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Step 3: Trigram search (retrieve 2*topK candidates)
	scopeFilter := domain.BuildScopeFilter(req.UserID, req.AgentID, "user")
	trigramFacts, err := s.factRepo.SearchByContent(ctx, scopeFilter, req.Query, req.TopK*2)
	if err != nil {
		return nil, fmt.Errorf("trigram search: %w", err)
	}

	// Step 4: Build rank maps
	vectorRanks := make(map[string]int)
	for i, doc := range vectorDocs {
		vectorRanks[doc.ID] = i + 1 // rank starts at 1
	}

	trigramRanks := make(map[string]int)
	for i, fact := range trigramFacts {
		trigramRanks[fact.ID] = i + 1
	}

	// Step 5: Collect all unique fact IDs
	allIDs := make(map[string]bool)
	for _, doc := range vectorDocs {
		allIDs[doc.ID] = true
	}
	for _, fact := range trigramFacts {
		allIDs[fact.ID] = true
	}

	// Step 6: Calculate RRF score for each fact
	k := float64(constants.MemoryRRFConstant)
	var scored []scoredFact

	for id := range allIDs {
		vectorRank := vectorRanks[id]
		trigramRank := trigramRanks[id]

		// RRF formula: score = 1/(k+rank_vector) + 1/(k+rank_trigram)
		rrfScore := 0.0
		if vectorRank > 0 {
			rrfScore += 1.0 / (k + float64(vectorRank))
		}
		if trigramRank > 0 {
			rrfScore += 1.0 / (k + float64(trigramRank))
		}

		// Fetch full fact
		fact, err := s.factRepo.GetByID(ctx, id)
		if err != nil {
			continue // skip if not found
		}

		scored = append(scored, scoredFact{fact: fact, score: rrfScore})
	}

	// Step 7: Sort by RRF score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Step 8: Take top-K and increment access_count
	topK := req.TopK
	if topK > len(scored) {
		topK = len(scored)
	}

	var dtos []FactDTO
	for i := 0; i < topK; i++ {
		fact := scored[i].fact

		// Increment access count (best-effort, don't fail recall on update error)
		fact.AccessCount++
		fact.LastAccessAt = time.Now()
		_ = s.factRepo.Update(ctx, fact)

		dtos = append(dtos, FactDTO{
			ID:          fact.ID,
			Content:     fact.Content,
			Importance:  fact.Importance,
			Keywords:    nil, // keywords not stored in current schema
			EntityNames: fact.EntityNames,
			AccessCount: fact.AccessCount,
			CreatedAt:   fact.CreatedAt,
		})
	}

	return &RecallMemoryResponse{Facts: dtos}, nil
}

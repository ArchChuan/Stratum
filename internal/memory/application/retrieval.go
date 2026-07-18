package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
)

var ErrInvalidRecallMemoryRequest = errors.New("invalid recall memory request")

type scoredFact struct {
	fact  *domain.MemoryFact
	score float64
}

// RecallMemory performs hybrid retrieval: vector search + trigram search + RRF fusion.
func (s *MemoryService) RecallMemory(ctx context.Context, req *RecallMemoryRequest) (*RecallMemoryResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidRecallMemoryRequest)
	}
	query := strings.TrimSpace(req.Query)
	if req.TenantID == "" || req.UserID == "" || query == "" || req.TopK <= 0 {
		return nil, fmt.Errorf("%w: tenant ID, user ID, query, and positive top K are required", ErrInvalidRecallMemoryRequest)
	}
	// Step 1: Embed query for vector search
	queryVector, err := s.embedClient.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Step 2: Vector search (retrieve 2*topK candidates)
	collectionName := fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(req.TenantID, "-", "_"))
	filter := port.VectorSearchFilter{
		UserID: req.UserID, AgentID: req.AgentID, IncludeUserScope: true, IncludeAgentScope: req.AgentID != "",
	}

	vectorDocs, err := s.vectorStore.Search(ctx, collectionName, queryVector, req.TopK*2, filter)
	if err != nil {
		var unavailable *port.VectorStoreUnavailableError
		if !errors.As(err, &unavailable) {
			return nil, fmt.Errorf("vector search: %w", err)
		}
		vectorDocs = nil
	}

	// Step 3: Trigram search (retrieve 2*topK candidates)
	scopeFilter := domain.BuildScopeFilter(req.TenantID, req.UserID, req.AgentID, "user")
	trigramFacts, err := s.factRepo.SearchByContent(ctx, req.TenantID, scopeFilter, query, req.TopK*2)
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
		fact, err := s.factRepo.GetByID(ctx, req.TenantID, id)
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

		// Increment access count and refresh frecency score (best-effort, don't fail recall on update error)
		fact.AccessCount++
		fact.LastAccessAt = timeutil.Now()
		daysSince := timeutil.Now().Sub(fact.CreatedAt).Hours() / 24
		fact.FrecencyScore = domain.CalculateFrecency(fact.Importance, daysSince, fact.AccessCount)
		_ = s.factRepo.Update(ctx, req.TenantID, fact)

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

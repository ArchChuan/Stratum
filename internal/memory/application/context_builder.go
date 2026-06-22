package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type scoredFactForContext struct {
	fact  *domain.MemoryFact
	score float64
}

// BuildContext returns frecency-ranked facts + entity profiles for prompt injection.
func (s *MemoryService) BuildContext(ctx context.Context, req *BuildContextRequest) (*BuildContextResponse, error) {
	// Default to "user" scope if not specified
	readScope := req.ReadScope
	if readScope == "" {
		readScope = "user"
	}

	filter := domain.BuildScopeFilter(req.UserID, req.AgentID, readScope)

	// Step 1: Fetch active facts (over-fetch for frecency ranking)
	facts, err := s.factRepo.ListActive(ctx, filter, 50)
	if err != nil {
		return nil, fmt.Errorf("list active facts: %w", err)
	}

	// Step 2: Calculate frecency scores
	var scored []scoredFactForContext
	now := time.Now()

	for _, fact := range facts {
		daysSinceAccess := now.Sub(fact.LastAccessAt).Hours() / 24
		frecency := domain.CalculateFrecency(fact.Importance, daysSinceAccess, fact.AccessCount)
		scored = append(scored, scoredFactForContext{fact: fact, score: frecency})
	}

	// Step 3: Sort by frecency descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Step 4: Take top-K
	topK := req.TopK
	if topK > len(scored) {
		topK = len(scored)
	}

	var contextLines []string
	var factDTOs []FactDTO

	for i := 0; i < topK; i++ {
		fact := scored[i].fact
		contextLines = append(contextLines, fmt.Sprintf("- %s", fact.Content))

		factDTOs = append(factDTOs, FactDTO{
			ID:          fact.ID,
			Content:     fact.Content,
			Importance:  fact.Importance,
			Keywords:    nil, // not stored in current schema
			EntityNames: fact.EntityNames,
			AccessCount: fact.AccessCount,
			CreatedAt:   fact.CreatedAt,
		})
	}

	// Step 5: Fetch entity profiles
	entities, err := s.entityRepo.ListProfiles(ctx, filter, 10)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}

	var profileDTOs []EntityProfileDTO
	for _, entity := range entities {
		if entity.Profile != "" {
			profileDTOs = append(profileDTOs, EntityProfileDTO{
				Name:    entity.Name,
				Type:    entity.EntityType,
				Profile: entity.Profile,
			})
		}
	}

	// Step 6: Build context text
	contextText := strings.Join(contextLines, "\n")

	return &BuildContextResponse{
		Facts:          factDTOs,
		EntityProfiles: profileDTOs,
		ContextText:    contextText,
	}, nil
}

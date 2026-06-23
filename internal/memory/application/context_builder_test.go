package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestBuildContext_FrecencyRanking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping context builder test")
	}

	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	// Create facts with different frecency patterns
	now := time.Now()
	fact1, _ := domain.NewFact("", "user1", "agent1", "user", "Recent high-importance fact", 0.9, []string{})
	fact1.AccessCount = 5
	fact1.LastAccessAt = now.Add(-1 * 24 * time.Hour) // 1 day ago

	fact2, _ := domain.NewFact("", "user1", "agent1", "user", "Old low-importance fact", 0.3, []string{})
	fact2.AccessCount = 1
	fact2.LastAccessAt = now.Add(-30 * 24 * time.Hour) // 30 days ago

	fact3, _ := domain.NewFact("", "user1", "agent1", "user", "Medium fact", 0.6, []string{})
	fact3.AccessCount = 3
	fact3.LastAccessAt = now.Add(-7 * 24 * time.Hour) // 7 days ago

	// Mock fact retrieval
	factRepo.On("ListActive", ctx, "tenant1", mock.AnythingOfType("domain.ScopeFilter"), 50).
		Return([]*domain.MemoryFact{fact1, fact2, fact3}, nil)

	// Mock entity profiles
	entity, _ := domain.NewEntity("user1", "agent1", "user", "Python", "technology")
	entity.Profile = "User's preferred programming language"
	entityRepo.On("ListProfiles", ctx, mock.AnythingOfType("domain.ScopeFilter"), 10).
		Return([]*domain.MemoryEntity{entity}, nil)

	req := &BuildContextRequest{
		TenantID:  "tenant1",
		UserID:    "user1",
		AgentID:   "agent1",
		Query:     "test",
		TopK:      3,
		ReadScope: "user",
	}

	resp, err := svc.BuildContext(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Facts, 3)
	assert.Len(t, resp.EntityProfiles, 1)
	assert.Contains(t, resp.ContextText, "Recent high-importance fact")
	assert.Equal(t, "Python", resp.EntityProfiles[0].Name)

	// Verify fact1 has highest frecency (most recent + high importance)
	assert.Equal(t, fact1.ID, resp.Facts[0].ID)

	factRepo.AssertExpectations(t)
	entityRepo.AssertExpectations(t)
}

func TestBuildContext_EmptyFacts(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	// No facts
	factRepo.On("ListActive", ctx, "tenant1", mock.AnythingOfType("domain.ScopeFilter"), 50).
		Return([]*domain.MemoryFact{}, nil)

	// No entities
	entityRepo.On("ListProfiles", ctx, mock.AnythingOfType("domain.ScopeFilter"), 10).
		Return([]*domain.MemoryEntity{}, nil)

	req := &BuildContextRequest{
		TenantID:  "tenant1",
		UserID:    "user1",
		AgentID:   "agent1",
		Query:     "test",
		TopK:      10,
		ReadScope: "user",
	}

	resp, err := svc.BuildContext(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Facts)
	assert.Empty(t, resp.EntityProfiles)
	assert.Equal(t, "", resp.ContextText)

	factRepo.AssertExpectations(t)
	entityRepo.AssertExpectations(t)
}

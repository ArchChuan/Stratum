package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

func TestRecallMemory_HybridRetrieval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hybrid retrieval test")
	}

	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	// Mock embedding
	embedClient.On("Embed", ctx, "Python programming").
		Return([]float32{0.1, 0.2, 0.3}, nil)

	// Mock vector search (returns 2 docs)
	fact1, _ := domain.NewFact("", "user1", "agent1", "user", "Python is great", 0.8, []string{"Python"})
	fact2, _ := domain.NewFact("", "user1", "agent1", "user", "Go is fast", 0.7, []string{"Go"})

	vectorStore.On("Search", ctx, "memory_facts_tenant1", mock.Anything, 20, mock.Anything).
		Return([]*port.VectorDoc{
			{ID: fact1.ID, Similarity: 0.9},
			{ID: fact2.ID, Similarity: 0.7},
		}, nil)

	// Mock trigram search (returns 2 facts, 1 overlap)
	fact3, _ := domain.NewFact("", "user1", "agent1", "user", "Python for ML", 0.75, []string{"Python"})

	factRepo.On("SearchByContent", ctx, "tenant1", mock.AnythingOfType("domain.ScopeFilter"), "Python programming", 20).
		Return([]*domain.MemoryFact{fact1, fact3}, nil)

	// Mock GetByID for RRF fusion
	factRepo.On("GetByID", ctx, "tenant1", fact1.ID).Return(fact1, nil)
	factRepo.On("GetByID", ctx, "tenant1", fact2.ID).Return(fact2, nil)
	factRepo.On("GetByID", ctx, "tenant1", fact3.ID).Return(fact3, nil)

	// Mock Update for access count
	factRepo.On("Update", ctx, "tenant1", mock.AnythingOfType("*domain.MemoryFact")).Return(nil).Times(3)

	req := &RecallMemoryRequest{
		TenantID: "tenant1",
		UserID:   "user1",
		AgentID:  "agent1",
		Query:    "Python programming",
		TopK:     10,
	}

	resp, err := svc.RecallMemory(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Facts, 3)                // 3 unique facts
	assert.Equal(t, fact1.ID, resp.Facts[0].ID) // highest RRF score (appears in both)

	embedClient.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

func TestRecallMemory_EmptyResults(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	embedClient.On("Embed", ctx, "nonexistent query").
		Return([]float32{0.1, 0.2, 0.3}, nil)

	vectorStore.On("Search", ctx, "memory_facts_tenant1", mock.Anything, 20, mock.Anything).
		Return([]*port.VectorDoc{}, nil)

	factRepo.On("SearchByContent", ctx, "tenant1", mock.AnythingOfType("domain.ScopeFilter"), "nonexistent query", 20).
		Return([]*domain.MemoryFact{}, nil)

	req := &RecallMemoryRequest{
		TenantID: "tenant1",
		UserID:   "user1",
		AgentID:  "agent1",
		Query:    "nonexistent query",
		TopK:     10,
	}

	resp, err := svc.RecallMemory(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Facts)

	embedClient.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

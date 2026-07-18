package application

import (
	"context"
	"errors"
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

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil, nil)

	// Mock embedding
	embedClient.On("Embed", ctx, "Python programming").
		Return([]float32{0.1, 0.2, 0.3}, nil)

	// Mock vector search (returns 2 docs)
	fact1, _ := domain.NewFact("", "user1", "agent1", "", "user", "Python is great", 0.8, []string{"Python"})
	fact2, _ := domain.NewFact("", "user1", "agent1", "", "user", "Go is fast", 0.7, []string{"Go"})

	vectorStore.On("Search", ctx, "memory_facts_tenant1", mock.Anything, 20, mock.Anything).
		Return([]*port.VectorDoc{
			{ID: fact1.ID, Similarity: 0.9},
			{ID: fact2.ID, Similarity: 0.7},
		}, nil)

	// Mock trigram search (returns 2 facts, 1 overlap)
	fact3, _ := domain.NewFact("", "user1", "agent1", "", "user", "Python for ML", 0.75, []string{"Python"})

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

func TestRecallMemoryUsesScopeSafeVectorFilter(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	vectors := new(MockVectorStore)
	embed := new(MockEmbedClient)
	svc := NewMemoryService(facts, nil, nil, vectors, nil, embed, nil, nil)

	embed.On("Embed", ctx, "query").Return([]float32{1}, nil).Once()
	vectors.On("Search", ctx, "memory_facts_tenant_1", mock.Anything, 10,
		port.VectorSearchFilter{UserID: "user-1", AgentID: "agent-1", IncludeUserScope: true, IncludeAgentScope: true}).
		Return([]*port.VectorDoc{}, nil).Once()
	facts.On("SearchByContent", ctx, "tenant-1", mock.MatchedBy(func(filter domain.ScopeFilter) bool {
		return filter.UserID == "user-1" && filter.AgentID == "agent-1" && filter.IncludeUserScope && filter.IncludeAgentScope
	}), "query", 10).Return([]*domain.MemoryFact{}, nil).Once()

	_, err := svc.RecallMemory(ctx, &RecallMemoryRequest{TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", Query: "query", TopK: 5})
	assert.NoError(t, err)
	embed.AssertExpectations(t)
	vectors.AssertExpectations(t)
	facts.AssertExpectations(t)
}

func TestRecallMemoryFallsBackOnlyForVectorStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name        string
		vectorErr   error
		wantErr     bool
		wantTrigram bool
	}{
		{name: "unavailable", vectorErr: &port.VectorStoreUnavailableError{Err: errors.New("grpc unavailable")}, wantTrigram: true},
		{name: "schema error", vectorErr: errors.New("schema mismatch"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts := new(MockFactRepo)
			vectors := new(MockVectorStore)
			embed := new(MockEmbedClient)
			svc := NewMemoryService(facts, nil, nil, vectors, nil, embed, nil, nil)
			embed.On("Embed", ctx, "query").Return([]float32{1}, nil).Once()
			vectors.On("Search", ctx, "memory_facts_tenant", mock.Anything, 10, mock.Anything).
				Return([]*port.VectorDoc(nil), tt.vectorErr).Once()
			if tt.wantTrigram {
				facts.On("SearchByContent", ctx, "tenant", mock.AnythingOfType("domain.ScopeFilter"), "query", 10).
					Return([]*domain.MemoryFact{}, nil).Once()
			}
			_, err := svc.RecallMemory(ctx, &RecallMemoryRequest{TenantID: "tenant", UserID: "user", Query: "query", TopK: 5})
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			facts.AssertExpectations(t)
		})
	}
}

func TestRecallMemoryRejectsInvalidRequestsBeforeDependencies(t *testing.T) {
	tests := []struct {
		name string
		req  *RecallMemoryRequest
	}{
		{name: "nil request"},
		{name: "empty tenant", req: &RecallMemoryRequest{UserID: "user", Query: "query", TopK: 1}},
		{name: "empty user", req: &RecallMemoryRequest{TenantID: "tenant", Query: "query", TopK: 1}},
		{name: "empty query", req: &RecallMemoryRequest{TenantID: "tenant", UserID: "user", Query: "  ", TopK: 1}},
		{name: "zero top k", req: &RecallMemoryRequest{TenantID: "tenant", UserID: "user", Query: "query"}},
		{name: "negative top k", req: &RecallMemoryRequest{TenantID: "tenant", UserID: "user", Query: "query", TopK: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := new(MockFactRepo)
			vectors := new(MockVectorStore)
			embed := new(MockEmbedClient)
			svc := NewMemoryService(facts, nil, nil, vectors, nil, embed, nil, nil)
			_, err := svc.RecallMemory(context.Background(), tt.req)
			assert.ErrorIs(t, err, ErrInvalidRecallMemoryRequest)
			facts.AssertNotCalled(t, "SearchByContent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			vectors.AssertNotCalled(t, "Search", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			embed.AssertNotCalled(t, "Embed", mock.Anything, mock.Anything)
		})
	}
}

func TestRecallMemory_EmptyResults(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil, nil)

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

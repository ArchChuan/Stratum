package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

func TestExtractFacts_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extraction orchestration test")
	}

	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	// Mock LLM extraction
	llmExtract.On("ExtractFacts", ctx, "user1", "agent1", mock.Anything).Return([]*port.ExtractedFact{
		{
			Content:    "User prefers Python for backend work",
			Importance: 0.8,
			Entities:   []string{"Python"},
		},
	}, nil)

	// Mock supersede candidates (none found)
	factRepo.On("FindSupersedeCandidates", ctx, "user1", "agent1", mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.MemoryFact{}, nil)

	// Mock entity normalization (new entity)
	entityRepo.On("FindByNameAndType", ctx, "user1", "Python", "", mock.Anything).
		Return(nil, domain.ErrEntityNotFound)
	entityRepo.On("Create", ctx, mock.AnythingOfType("*domain.MemoryEntity")).Return(nil)

	// Mock fact insertion
	factRepo.On("Create", ctx, mock.AnythingOfType("*domain.MemoryFact")).Return(nil)

	// Mock embedding
	embedClient.On("Embed", ctx, "User prefers Python for backend work").
		Return([]float32{0.1, 0.2, 0.3}, nil)

	// Mock vector store
	vectorStore.On("Upsert", ctx, "memory_facts_tenant1", mock.AnythingOfType("[]*port.VectorDoc")).
		Return(nil)

	req := &ExtractFactsRequest{
		TenantID: "tenant1",
		UserID:   "user1",
		AgentID:  "agent1",
		Messages: []MessageDTO{
			{Role: "user", Content: "I like Python"},
		},
	}

	err := svc.ExtractFacts(ctx, req)
	assert.NoError(t, err)

	llmExtract.AssertExpectations(t)
	factRepo.AssertExpectations(t)
	entityRepo.AssertExpectations(t)
	embedClient.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
}

func TestExtractFacts_EntityUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extraction orchestration test")
	}

	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil)

	// Mock LLM extraction
	llmExtract.On("ExtractFacts", ctx, "user1", "agent1", mock.Anything).Return([]*port.ExtractedFact{
		{
			Content:    "User uses Go for microservices",
			Importance: 0.7,
			Entities:   []string{"Go"},
		},
	}, nil)

	// Mock supersede candidates (none)
	factRepo.On("FindSupersedeCandidates", ctx, "user1", "agent1", mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.MemoryFact{}, nil)

	// Mock entity normalization (existing entity)
	existingEntity, _ := domain.NewEntity("user1", "agent1", "user", "Go", "technology")
	entityRepo.On("FindByNameAndType", ctx, "user1", "Go", "", mock.Anything).
		Return(existingEntity, nil)
	entityRepo.On("Update", ctx, mock.AnythingOfType("*domain.MemoryEntity")).Return(nil)

	// Mock fact insertion
	factRepo.On("Create", ctx, mock.AnythingOfType("*domain.MemoryFact")).Return(nil)

	// Mock embedding
	embedClient.On("Embed", ctx, "User uses Go for microservices").
		Return([]float32{0.4, 0.5, 0.6}, nil)

	// Mock vector store
	vectorStore.On("Upsert", ctx, "memory_facts_tenant1", mock.AnythingOfType("[]*port.VectorDoc")).
		Return(nil)

	req := &ExtractFactsRequest{
		TenantID: "tenant1",
		UserID:   "user1",
		AgentID:  "agent1",
		Messages: []MessageDTO{
			{Role: "user", Content: "I use Go a lot"},
		},
	}

	err := svc.ExtractFacts(ctx, req)
	assert.NoError(t, err)

	llmExtract.AssertExpectations(t)
	factRepo.AssertExpectations(t)
	entityRepo.AssertExpectations(t)
	embedClient.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
}

func TestNormalizeEntity_NewEntity(t *testing.T) {
	ctx := context.Background()
	entityRepo := new(MockEntityRepo)

	svc := &MemoryService{
		entityRepo: entityRepo,
	}

	// No existing entity
	entityRepo.On("FindByNameAndType", ctx, "user1", "Python", "", mock.Anything).
		Return(nil, domain.ErrEntityNotFound)
	entityRepo.On("Create", ctx, mock.AnythingOfType("*domain.MemoryEntity")).Return(nil)

	id, err := svc.normalizeEntity(ctx, "user1", "agent1", "Python")
	assert.NoError(t, err)
	assert.NotEmpty(t, id)

	entityRepo.AssertExpectations(t)
}

func TestNormalizeEntity_ExistingEntity(t *testing.T) {
	ctx := context.Background()
	entityRepo := new(MockEntityRepo)

	svc := &MemoryService{
		entityRepo: entityRepo,
	}

	existing, _ := domain.NewEntity("user1", "agent1", "user", "Python", "technology")
	entityRepo.On("FindByNameAndType", ctx, "user1", "Python", "", mock.Anything).
		Return(existing, nil)
	entityRepo.On("Update", ctx, mock.AnythingOfType("*domain.MemoryEntity")).Return(nil)

	id, err := svc.normalizeEntity(ctx, "user1", "agent1", "Python")
	assert.NoError(t, err)
	assert.Equal(t, existing.ID, id)

	entityRepo.AssertExpectations(t)
}

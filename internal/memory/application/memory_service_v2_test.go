package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// Mock implementations for testing
type MockFactRepo struct {
	mock.Mock
}

func (m *MockFactRepo) Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	args := m.Called(ctx, tenantID, fact)
	return args.Error(0)
}

func (m *MockFactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryFact), args.Error(1)
}

func (m *MockFactRepo) Update(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	args := m.Called(ctx, tenantID, fact)
	return args.Error(0)
}

func (m *MockFactRepo) ListActive(ctx context.Context, tenantID string, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, filter, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryFact), args.Error(1)
}

func (m *MockFactRepo) SearchByContent(ctx context.Context, tenantID string, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, filter, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryFact), args.Error(1)
}

func (m *MockFactRepo) FindSupersedeCandidates(ctx context.Context, tenantID, userID, agentID, content string, minSimilarity, maxCount float64) ([]*port.SupersedeCandidate, error) {
	args := m.Called(ctx, tenantID, userID, agentID, content, minSimilarity, maxCount)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*port.SupersedeCandidate), args.Error(1)
}

func (m *MockFactRepo) CountByUser(ctx context.Context, tenantID, userID string) (int, error) {
	args := m.Called(ctx, tenantID, userID)
	return args.Int(0), args.Error(1)
}

func (m *MockFactRepo) Delete(ctx context.Context, tenantID, id string) error {
	args := m.Called(ctx, tenantID, id)
	return args.Error(0)
}

func (m *MockFactRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) ([]string, error) {
	args := m.Called(ctx, tenantID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockFactRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) ([]string, error) {
	args := m.Called(ctx, tenantID, agentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

type MockEntityRepo struct {
	mock.Mock
}

func (m *MockEntityRepo) Create(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	args := m.Called(ctx, tenantID, entity)
	return args.Error(0)
}

func (m *MockEntityRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryEntity, error) {
	args := m.Called(ctx, tenantID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryEntity), args.Error(1)
}

func (m *MockEntityRepo) Update(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	args := m.Called(ctx, tenantID, entity)
	return args.Error(0)
}

func (m *MockEntityRepo) FindByNameAndType(ctx context.Context, tenantID, userID, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	args := m.Called(ctx, tenantID, userID, name, entityType, threshold)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryEntity), args.Error(1)
}

func (m *MockEntityRepo) ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
	args := m.Called(ctx, filter, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryEntity), args.Error(1)
}

func (m *MockEntityRepo) CountByUser(ctx context.Context, tenantID, userID string) (int, error) {
	args := m.Called(ctx, tenantID, userID)
	return args.Int(0), args.Error(1)
}

func (m *MockEntityRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	args := m.Called(ctx, tenantID, userID)
	return args.Error(0)
}

func (m *MockEntityRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	args := m.Called(ctx, tenantID, agentID)
	return args.Error(0)
}

type MockExtractionQueue struct {
	mock.Mock
}

func (m *MockExtractionQueue) Enqueue(ctx context.Context, tenantID string, task *port.ExtractionTask) error {
	args := m.Called(ctx, tenantID, task)
	return args.Error(0)
}

func (m *MockExtractionQueue) Dequeue(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
	args := m.Called(ctx, tenantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*port.ExtractionTask), args.Error(1)
}

func (m *MockExtractionQueue) MarkCompleted(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time) error {
	args := m.Called(ctx, tenantID, taskID, claimedAt)
	return args.Error(0)
}

func (m *MockExtractionQueue) MarkFailed(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time, errMsg string) error {
	args := m.Called(ctx, tenantID, taskID, claimedAt, errMsg)
	return args.Error(0)
}

func (m *MockExtractionQueue) DeleteOldCompleted(ctx context.Context, tenantID string, retentionDays int) (int, error) {
	args := m.Called(ctx, tenantID, retentionDays)
	return args.Int(0), args.Error(1)
}

type MockVectorStore struct {
	mock.Mock
}

func (m *MockVectorStore) Upsert(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
	args := m.Called(ctx, collectionName, docs)
	return args.Error(0)
}

func (m *MockVectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter map[string]interface{}) ([]*port.VectorDoc, error) {
	args := m.Called(ctx, collectionName, queryVector, topK, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*port.VectorDoc), args.Error(1)
}

func (m *MockVectorStore) Delete(ctx context.Context, collectionName string, ids []string) error {
	args := m.Called(ctx, collectionName, ids)
	return args.Error(0)
}

func (m *MockVectorStore) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	args := m.Called(ctx, tenantID, userID)
	return args.Error(0)
}

func (m *MockVectorStore) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	args := m.Called(ctx, tenantID, agentID)
	return args.Error(0)
}

func (m *MockVectorStore) CreateCollection(ctx context.Context, collectionName string, dimension int) error {
	args := m.Called(ctx, collectionName, dimension)
	return args.Error(0)
}

type MockLLMExtractor struct {
	mock.Mock
}

func (m *MockLLMExtractor) ExtractFacts(ctx context.Context, userID, agentID, message string) ([]*port.ExtractedFact, error) {
	args := m.Called(ctx, userID, agentID, message)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*port.ExtractedFact), args.Error(1)
}

type MockEmbedClient struct {
	mock.Mock
}

func (m *MockEmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	args := m.Called(ctx, text)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]float32), args.Error(1)
}

func (m *MockEmbedClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	args := m.Called(ctx, texts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([][]float32), args.Error(1)
}

func TestNewMemoryService(t *testing.T) {
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	// No Redis client for unit test — buffer will be nil but service should construct
	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil, nil)

	assert.NotNil(t, svc)
	assert.Equal(t, factRepo, svc.factRepo)
	assert.Equal(t, entityRepo, svc.entityRepo)
	assert.Equal(t, queue, svc.queue)
	assert.Equal(t, vectorStore, svc.vectorStore)
	assert.Equal(t, llmExtract, svc.llmExtract)
	assert.Equal(t, embedClient, svc.embedClient)
}

func TestBufferMessageRequest_Fields(t *testing.T) {
	now := time.Now()
	req := &BufferMessageRequest{
		TenantID:       "tenant1",
		UserID:         "user1",
		AgentID:        "agent1",
		ConversationID: "conv1",
		MessageID:      "msg1",
		Role:           "user",
		Content:        "test content",
		CreatedAt:      now,
	}

	assert.Equal(t, "tenant1", req.TenantID)
	assert.Equal(t, "user1", req.UserID)
	assert.Equal(t, "agent1", req.AgentID)
	assert.Equal(t, "conv1", req.ConversationID)
	assert.Equal(t, "msg1", req.MessageID)
	assert.Equal(t, "user", req.Role)
	assert.Equal(t, "test content", req.Content)
	assert.Equal(t, now, req.CreatedAt)
}

func TestFactDTO_Fields(t *testing.T) {
	now := time.Now()
	dto := &FactDTO{
		ID:          "fact1",
		Content:     "test fact",
		Importance:  0.8,
		Keywords:    []string{"key1", "key2"},
		EntityNames: []string{"entity1"},
		AccessCount: 5,
		CreatedAt:   now,
	}

	assert.Equal(t, "fact1", dto.ID)
	assert.Equal(t, "test fact", dto.Content)
	assert.Equal(t, 0.8, dto.Importance)
	assert.Equal(t, []string{"key1", "key2"}, dto.Keywords)
	assert.Equal(t, []string{"entity1"}, dto.EntityNames)
	assert.Equal(t, 5, dto.AccessCount)
	assert.Equal(t, now, dto.CreatedAt)
}

func TestForgetMemoryDeletesFactVectorReplica(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, nil, nil, nil, nil)
	req := &ForgetMemoryRequest{
		TenantID: "42c9b62d-4f66-4bc4-a1b8-eed81cdae7b1",
		UserID:   "user-1",
		FactID:   "fact-1",
	}

	factRepo.On("Delete", ctx, req.TenantID, req.FactID).Return(nil).Once()
	vectorStore.On("Delete", ctx, "memory_facts_42c9b62d_4f66_4bc4_a1b8_eed81cdae7b1", []string{req.FactID}).Return(nil).Once()

	err := svc.ForgetMemory(ctx, req)

	assert.NoError(t, err)
	factRepo.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
}

package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

func TestMemoryServiceClearUserMemoriesReturnsVectorCleanupError(t *testing.T) {
	ctx := context.Background()
	facts, vectors := new(MockFactRepo), new(MockVectorStore)
	svc := NewMemoryService(facts, nil, nil, vectors, nil, nil, nil, nil)
	wantErr := errors.New("milvus unavailable")
	facts.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return([]string{"fact-1"}, nil).Once()
	vectors.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return(wantErr).Once()

	err := svc.ClearUserMemories(ctx, &ClearUserMemoriesRequest{TenantID: "tenant-1", UserID: "user-1"})
	assert.ErrorIs(t, err, wantErr)
}

func TestMemoryServiceClearAgentMemoriesUsesBulkVectorCleanupAndReturnsItsError(t *testing.T) {
	ctx := context.Background()
	facts, vectors := new(MockFactRepo), new(MockVectorStore)
	svc := NewMemoryService(facts, nil, nil, vectors, nil, nil, nil, nil)
	wantErr := errors.New("milvus unavailable")
	facts.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return([]string{"fact-1"}, nil).Once()
	vectors.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return(wantErr).Once()

	err := svc.ClearAgentMemories(ctx, "tenant-1", "agent-1")
	assert.ErrorIs(t, err, wantErr)
	vectors.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything)
}

func TestMemoryServiceClearUserMemoriesAttemptsEveryStageAndJoinsErrors(t *testing.T) {
	ctx := context.Background()
	facts, vectors, memories, entities := new(MockFactRepo), new(MockVectorStore), new(cleanupMemoryRepo), new(MockEntityRepo)
	svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
	svc.SetMemoryRepo(memories)
	factErr := errors.New("facts failed")
	vectorErr := errors.New("vectors failed")
	memoryErr := errors.New("entries failed")
	entityErr := errors.New("entities failed")
	facts.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return(nil, factErr).Once()
	vectors.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return(vectorErr).Once()
	memories.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return(memoryErr).Once()
	entities.On("DeleteAllByUser", ctx, "tenant-1", "user-1").Return(entityErr).Once()

	err := svc.ClearUserMemories(ctx, &ClearUserMemoriesRequest{TenantID: "tenant-1", UserID: "user-1"})
	for _, want := range []error{factErr, vectorErr, memoryErr, entityErr} {
		assert.ErrorIs(t, err, want)
	}
	for _, operation := range []string{"clear user facts", "clear user vectors", "clear user memory entries", "clear user entities"} {
		assert.ErrorContains(t, err, operation)
	}
	facts.AssertExpectations(t)
	vectors.AssertExpectations(t)
	memories.AssertExpectations(t)
	entities.AssertExpectations(t)
}

func TestMemoryServiceClearAgentMemoriesAttemptsEveryStageAndJoinsErrors(t *testing.T) {
	ctx := context.Background()
	facts, vectors, memories, entities := new(MockFactRepo), new(MockVectorStore), new(cleanupMemoryRepo), new(MockEntityRepo)
	svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
	svc.SetMemoryRepo(memories)
	factErr := errors.New("facts failed")
	vectorErr := errors.New("vectors failed")
	memoryErr := errors.New("entries failed")
	entityErr := errors.New("entities failed")
	facts.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return(nil, factErr).Once()
	vectors.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return(vectorErr).Once()
	memories.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return(memoryErr).Once()
	entities.On("DeleteAllByAgent", ctx, "tenant-1", "agent-1").Return(entityErr).Once()

	err := svc.ClearAgentMemories(ctx, "tenant-1", "agent-1")
	for _, want := range []error{factErr, vectorErr, memoryErr, entityErr} {
		assert.ErrorIs(t, err, want)
	}
	for _, operation := range []string{"clear agent facts", "clear agent vectors", "clear agent memory entries", "clear agent entities"} {
		assert.ErrorContains(t, err, operation)
	}
	facts.AssertExpectations(t)
	vectors.AssertExpectations(t)
	memories.AssertExpectations(t)
	entities.AssertExpectations(t)
}

type cleanupMemoryRepo struct{ mock.Mock }

func (m *cleanupMemoryRepo) Add(context.Context, *domain.MemoryEntry) error { return nil }
func (m *cleanupMemoryRepo) Get(context.Context, string, string) (*domain.MemoryEntry, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) Search(context.Context, string, string, string, int) ([]*domain.MemoryEntry, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) Delete(context.Context, string, string) error       { return nil }
func (m *cleanupMemoryRepo) ClearSession(context.Context, string, string) error { return nil }
func (m *cleanupMemoryRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	return m.Called(ctx, tenantID, userID).Error(0)
}
func (m *cleanupMemoryRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	return m.Called(ctx, tenantID, agentID).Error(0)
}
func (m *cleanupMemoryRepo) Stats(context.Context, string) (*domain.MemoryStats, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) GetSummary(context.Context, string, string) (string, error) {
	return "", nil
}

// Mock implementations for testing
type MockFactRepo struct {
	mock.Mock
}

func (m *MockFactRepo) Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	args := m.Called(ctx, tenantID, fact)
	return args.Error(0)
}

func (m *MockFactRepo) CreateExtracted(ctx context.Context, tenantID string, write *port.ExtractedFactWrite) (*domain.MemoryFact, bool, error) {
	args := m.Called(ctx, tenantID, write)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*domain.MemoryFact), args.Bool(1), args.Error(2)
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

func (m *MockFactRepo) FindSupersedeCandidates(ctx context.Context, tenantID string, filter domain.ScopeFilter, content string, minSimilarity, maxCount float64) ([]*port.SupersedeCandidate, error) {
	args := m.Called(ctx, tenantID, filter, content, minSimilarity, maxCount)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*port.SupersedeCandidate), args.Error(1)
}

func (m *MockFactRepo) ListUserFacts(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryFact), args.Error(1)
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

func (m *MockFactRepo) PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) (int, error) {
	args := m.Called(ctx, tenantID, olderThan, limit)
	return args.Int(0), args.Error(1)
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

func (m *MockEntityRepo) FindByNameAndType(ctx context.Context, tenantID string, filter domain.ScopeFilter, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	args := m.Called(ctx, tenantID, filter, name, entityType, threshold)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryEntity), args.Error(1)
}

func (m *MockEntityRepo) ListUserEntities(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.MemoryEntity, error) {
	args := m.Called(ctx, tenantID, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryEntity), args.Error(1)
}

func (m *MockEntityRepo) CountUserEntities(ctx context.Context, tenantID, userID string) (int, error) {
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

func (m *MockVectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter port.VectorSearchFilter) ([]*port.VectorDoc, error) {
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

func TestMemoryService_ListUserMemories_NewestFirstWithActiveTotal(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)

	facts.On("ListUserFacts", ctx, "tenant-1", "user-1", 10, 20).Return([]*domain.MemoryFact{
		{ID: "fact-2", Scope: domain.ScopeUser, Content: "prefers Go", Importance: 0.8},
		{ID: "fact-1", Scope: domain.ScopeUser, Content: "likes Go", Importance: 0.7},
	}, nil).Once()
	facts.On("CountByUser", ctx, "tenant-1", "user-1").Return(42, nil).Once()

	memories, total, err := svc.ListUserMemories(ctx, &ListUserMemoriesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 10, Offset: 20,
	})
	assert.NoError(t, err)
	assert.Equal(t, 42, total)
	if len(memories) != 2 {
		t.Fatalf("len=%d, want 2", len(memories))
	}
	assert.Equal(t, "fact-2", memories[0].ID)
	assert.Equal(t, "user", memories[0].Scope)
	facts.AssertExpectations(t)
}

func TestMemoryService_ListUserMemories_PropagatesRepoErrors(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)

	facts.On("ListUserFacts", ctx, "tenant-1", "user-1", 20, 0).Return(nil, errors.New("list failed")).Once()
	_, _, err := svc.ListUserMemories(ctx, &ListUserMemoriesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 20, Offset: 0,
	})
	assert.ErrorContains(t, err, "list user memories")

	facts.On("ListUserFacts", ctx, "tenant-1", "user-1", 20, 0).Return(nil, nil).Once()
	facts.On("CountByUser", ctx, "tenant-1", "user-1").Return(0, errors.New("count failed")).Once()
	_, _, err = svc.ListUserMemories(ctx, &ListUserMemoriesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 20, Offset: 0,
	})
	assert.ErrorContains(t, err, "count user memories")
	facts.AssertExpectations(t)
}

func TestMemoryService_UserStats_returnsUserLevelCounts(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	entities := new(MockEntityRepo)
	svc := NewMemoryService(facts, entities, nil, nil, nil, nil, nil, nil)

	facts.On("CountByUser", ctx, "tenant-1", "user-1").Return(7, nil).Once()
	entities.On("CountUserEntities", ctx, "tenant-1", "user-1").Return(3, nil).Once()

	memoryCount, entityCount, err := svc.UserStats(ctx, "tenant-1", "user-1")
	assert.NoError(t, err)
	assert.Equal(t, 7, memoryCount)
	assert.Equal(t, 3, entityCount)
	facts.AssertExpectations(t)
	entities.AssertExpectations(t)
}

func TestMemoryService_UserStats_propagatesRepoErrors(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	entities := new(MockEntityRepo)
	svc := NewMemoryService(facts, entities, nil, nil, nil, nil, nil, nil)

	facts.On("CountByUser", ctx, "tenant-1", "user-1").Return(0, errors.New("facts failed")).Once()
	_, _, err := svc.UserStats(ctx, "tenant-1", "user-1")
	assert.ErrorContains(t, err, "count user memories")

	facts.On("CountByUser", ctx, "tenant-1", "user-1").Return(5, nil).Once()
	entities.On("CountUserEntities", ctx, "tenant-1", "user-1").Return(0, errors.New("entities failed")).Once()
	_, _, err = svc.UserStats(ctx, "tenant-1", "user-1")
	assert.ErrorContains(t, err, "count user entities")
	facts.AssertExpectations(t)
	entities.AssertExpectations(t)
}

func TestMemoryService_ListUserEntities_mapsToTopicTags(t *testing.T) {
	ctx := context.Background()
	entities := new(MockEntityRepo)
	svc := NewMemoryService(nil, entities, nil, nil, nil, nil, nil, nil)
	now := time.Now()

	entities.On("ListUserEntities", ctx, "tenant-1", "user-1", 10, 20).Return([]*domain.MemoryEntity{
		{ID: "ent-2", Name: "Go", EntityType: "tech", FactCount: 4, LastSeenAt: now.Add(time.Hour)},
		{ID: "ent-1", Name: "Alice", EntityType: "person", FactCount: 2, LastSeenAt: now},
	}, nil).Once()
	entities.On("CountUserEntities", ctx, "tenant-1", "user-1").Return(42, nil).Once()

	got, total, err := svc.ListUserEntities(ctx, &ListUserEntitiesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 10, Offset: 20,
	})
	assert.NoError(t, err)
	assert.Equal(t, 42, total)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	assert.Equal(t, "Go", got[0].Name)
	assert.Equal(t, "person", got[1].EntityType)
	assert.Equal(t, 2, got[1].FactCount)
	entities.AssertExpectations(t)
}

func TestMemoryService_ListUserEntities_propagatesRepoErrors(t *testing.T) {
	ctx := context.Background()
	entities := new(MockEntityRepo)
	svc := NewMemoryService(nil, entities, nil, nil, nil, nil, nil, nil)

	entities.On("ListUserEntities", ctx, "tenant-1", "user-1", 20, 0).Return(nil, errors.New("list failed")).Once()
	_, _, err := svc.ListUserEntities(ctx, &ListUserEntitiesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 20, Offset: 0,
	})
	assert.ErrorContains(t, err, "list user entities")

	entities.On("ListUserEntities", ctx, "tenant-1", "user-1", 20, 0).Return(nil, nil).Once()
	entities.On("CountUserEntities", ctx, "tenant-1", "user-1").Return(0, errors.New("count failed")).Once()
	_, _, err = svc.ListUserEntities(ctx, &ListUserEntitiesRequest{
		TenantID: "tenant-1", UserID: "user-1", Limit: 20, Offset: 0,
	})
	assert.ErrorContains(t, err, "count user entities")
	entities.AssertExpectations(t)
}

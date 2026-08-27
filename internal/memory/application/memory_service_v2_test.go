package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
func (m *cleanupMemoryRepo) ListExpired(context.Context, string, time.Time, time.Time, int) ([]string, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) DeleteByIDs(context.Context, string, []string) error {
	return nil
}
func (m *cleanupMemoryRepo) Stats(context.Context, string) (*domain.MemoryStats, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) GetSummary(context.Context, string, string) (string, error) {
	return "", nil
}
func (m *cleanupMemoryRepo) ListUserEntries(context.Context, string, string, int, int, string) ([]*domain.MemoryEntryListItem, error) {
	return nil, nil
}
func (m *cleanupMemoryRepo) CountUserEntries(context.Context, string, string, string) (int, error) {
	return 0, nil
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

func (m *MockFactRepo) ListUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter, limit, offset int) ([]*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, userID, filter, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryFact), args.Error(1)
}

func (m *MockFactRepo) CountUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter) (int, error) {
	args := m.Called(ctx, tenantID, userID, filter)
	return args.Int(0), args.Error(1)
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

func (m *MockFactRepo) PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) ([]string, error) {
	args := m.Called(ctx, tenantID, olderThan, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockFactRepo) CountAll(ctx context.Context, tenantID string) (int, error) {
	args := m.Called(ctx, tenantID)
	return args.Int(0), args.Error(1)
}

func (m *MockFactRepo) ListAllFacts(ctx context.Context, tenantID string, limit, offset int) ([]*domain.MemoryFact, error) {
	args := m.Called(ctx, tenantID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.MemoryFact), args.Error(1)
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

func (m *MockEntityRepo) Delete(ctx context.Context, tenantID, id string) error {
	args := m.Called(ctx, tenantID, id)
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

func (m *MockVectorStore) DeleteEntryVectors(ctx context.Context, tenantID string, ids []string) error {
	args := m.Called(ctx, tenantID, ids)
	return args.Error(0)
}

func (m *MockVectorStore) DeleteFactVectors(ctx context.Context, tenantID string, ids []string) error {
	args := m.Called(ctx, tenantID, ids)
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

// Model reports the fixed test model so collection names take the
// model-suffixed form ("memory_facts_<tenant>_text_embedding_v3").
func (m *MockEmbedClient) Model() string { return "text-embedding-v3" }

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

func newFactSvc() (*MemoryService, *MockFactRepo, *MockVectorStore, *MockEntityRepo, *MockEmbedClient) {
	facts, vectors, entities := new(MockFactRepo), new(MockVectorStore), new(MockEntityRepo)
	svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
	embed := new(MockEmbedClient)
	svc.SetEmbedClientResolver(func(context.Context, string) port.EmbedClient { return embed })
	return svc, facts, vectors, entities, embed
}

func TestMemoryService_UpdateUserFact(t *testing.T) {
	t.Run("success re-embeds and syncs vectors", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, vectors, _, embed := newFactSvc()
		var order []string
		fact := &domain.MemoryFact{ID: "fact-1", UserID: "user-1", Content: "I prefer dark mode",
			Importance: 0.8, Category: "preference", Status: domain.FactStatusActive}
		newContent := "I prefer light mode"
		facts.On("GetByID", ctx, "tenant-1", "fact-1").Return(fact, nil).Once()
		embed.On("Embed", ctx, newContent).Return([]float32{0.1, 0.2}, nil).
			Run(func(mock.Arguments) { order = append(order, "embed") }).Once()
		vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(nil).
			Run(func(mock.Arguments) { order = append(order, "delete_vectors") }).Once()
		facts.On("Update", ctx, "tenant-1", mock.MatchedBy(func(f *domain.MemoryFact) bool {
			return f.ID == "fact-1" && f.Content == newContent && f.Category == "preference"
		})).Return(nil).
			Run(func(mock.Arguments) { order = append(order, "update") }).Once()
		vectors.On("Upsert", ctx, "memory_facts_tenant_1_text_embedding_v3", mock.Anything).Return(nil).
			Run(func(mock.Arguments) { order = append(order, "upsert") }).Once()

		got, vectorSyncFailed, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1",
			&UpdateUserFactPatch{Content: &newContent})
		require.NoError(t, err)
		require.False(t, vectorSyncFailed)
		require.Equal(t, newContent, got.Content)
		// 只 patch Content 时，未触及字段（Category 等）必须原样保留。
		require.Equal(t, "preference", got.Category)
		// spec §3 顺序：embed NEW → delete OLD vectors → PG update → upsert NEW vector。
		require.Equal(t, []string{"embed", "delete_vectors", "update", "upsert"}, order)
		facts.AssertExpectations(t)
		vectors.AssertExpectations(t)
		embed.AssertExpectations(t)
	})

	t.Run("rejects empty patch", func(t *testing.T) {
		svc, facts, _, _, _ := newFactSvc()
		_, _, err := svc.UpdateUserFact(context.Background(), "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{})
		require.ErrorIs(t, err, domain.ErrEmptyFactPatch)
		// fail-fast：空补丁不触达任何存储。
		facts.AssertNotCalled(t, "GetByID", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("rejects other users fact as 404", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, _, _, _ := newFactSvc()
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "other-user"}, nil).Once()
		content := "x"
		_, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
		require.ErrorIs(t, err, domain.ErrFactNotFound)
		facts.AssertExpectations(t)
	})

	t.Run("rejects non-active fact as 409", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, _, _, _ := newFactSvc()
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Status: domain.FactStatusSuperseded}, nil).Once()
		content := "x"
		_, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
		require.ErrorIs(t, err, domain.ErrFactNotEditable)
		facts.AssertExpectations(t)
	})

	t.Run("fails closed when embedder unavailable", func(t *testing.T) {
		ctx := context.Background()
		facts := new(MockFactRepo)
		svc := NewMemoryService(facts, new(MockEntityRepo), nil, new(MockVectorStore), nil, nil, nil, nil)
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Status: domain.FactStatusActive}, nil).Once()
		content := "x"
		_, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
		require.ErrorIs(t, err, ErrMemoryEmbeddingUnavailable)
		facts.AssertExpectations(t)
	})

	t.Run("fails closed when embed api errors", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, vectors, _, embed := newFactSvc()
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Content: "old", Status: domain.FactStatusActive}, nil).Once()
		embed.On("Embed", ctx, "new").Return(nil, errors.New("embed api down")).Once()

		content := "new"
		_, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
		require.ErrorIs(t, err, ErrMemoryEmbeddingUnavailable)
		require.ErrorContains(t, err, "embed api down")
		// fail-closed：嵌入失败时不得删旧向量、写 PG 或同步新向量。
		vectors.AssertNotCalled(t, "DeleteFactVectors", mock.Anything, mock.Anything, mock.Anything)
		facts.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
		facts.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything)
		vectors.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything, mock.Anything)
		facts.AssertExpectations(t)
		embed.AssertExpectations(t)
	})

	t.Run("reports vector sync failure but keeps pg change", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, vectors, _, embed := newFactSvc()
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Content: "old", Status: domain.FactStatusActive}, nil).Once()
		embed.On("Embed", ctx, "new").Return([]float32{0.1}, nil).Once()
		vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(nil).Once()
		facts.On("Update", ctx, mock.Anything, mock.Anything).Return(nil).Once()
		vectors.On("Upsert", ctx, "memory_facts_tenant_1_text_embedding_v3", mock.Anything).Return(errors.New("milvus down")).Once()

		content := "new"
		got, vectorSyncFailed, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
		require.NoError(t, err)
		require.True(t, vectorSyncFailed)
		require.Equal(t, "new", got.Content)
		facts.AssertExpectations(t)
		vectors.AssertExpectations(t)
		embed.AssertExpectations(t)
	})
}

func TestMemoryService_DeleteUserFact(t *testing.T) {
	t.Run("deletes fact and best-effort clears vectors", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, vectors, _, _ := newFactSvc()
		facts.On("GetByID", ctx, "tenant-1", "fact-1").
			Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1"}, nil).Once()
		facts.On("Delete", ctx, "tenant-1", "fact-1").Return(nil).Once()
		// 向量清理失败不阻塞主操作。
		vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(errors.New("milvus down")).Once()
		require.NoError(t, svc.DeleteUserFact(ctx, "tenant-1", "user-1", "fact-1"))
		facts.AssertExpectations(t)
		vectors.AssertExpectations(t)
	})
}

func TestMemoryService_ListUserFactsFiltered(t *testing.T) {
	t.Run("returns filtered facts with explicit limit", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, _, _, _ := newFactSvc()
		filter := domain.FactListFilter{Query: "dark"}
		facts.On("ListUserFactsFiltered", ctx, "tenant-1", "user-1", filter, 20, 0).
			Return([]*domain.MemoryFact{{ID: "fact-1", UserID: "user-1", Category: "preference", Status: "active"}}, nil).Once()
		facts.On("CountUserFactsFiltered", ctx, "tenant-1", "user-1", filter).Return(1, nil).Once()

		got, total, err := svc.ListUserFactsFiltered(ctx, &ListUserFactsFilteredRequest{TenantID: "tenant-1", UserID: "user-1", Query: "dark", Limit: 20})
		require.NoError(t, err)
		require.Equal(t, 1, total)
		require.Len(t, got, 1)
		require.Equal(t, "fact-1", got[0].ID)
		facts.AssertExpectations(t)
	})

	t.Run("defaults to DefaultPageSize when limit <= 0", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, _, _, _ := newFactSvc()
		filter := domain.FactListFilter{Query: "dark"}
		facts.On("ListUserFactsFiltered", ctx, "tenant-1", "user-1", filter, constants.DefaultPageSize, 0).
			Return([]*domain.MemoryFact{{ID: "fact-1"}}, nil).Once()
		facts.On("CountUserFactsFiltered", ctx, "tenant-1", "user-1", filter).Return(1, nil).Once()

		got, total, err := svc.ListUserFactsFiltered(ctx, &ListUserFactsFilteredRequest{TenantID: "tenant-1", UserID: "user-1", Query: "dark", Limit: 0})
		require.NoError(t, err)
		require.Equal(t, 1, total)
		require.Len(t, got, 1)
		facts.AssertExpectations(t)
	})

	t.Run("returns empty non-nil slice on empty repo result", func(t *testing.T) {
		ctx := context.Background()
		svc, facts, _, _, _ := newFactSvc()
		filter := domain.FactListFilter{}
		facts.On("ListUserFactsFiltered", ctx, "tenant-1", "user-1", filter, constants.DefaultPageSize, 0).
			Return(nil, nil).Once()
		facts.On("CountUserFactsFiltered", ctx, "tenant-1", "user-1", filter).Return(0, nil).Once()

		got, total, err := svc.ListUserFactsFiltered(ctx, &ListUserFactsFilteredRequest{TenantID: "tenant-1", UserID: "user-1"})
		require.NoError(t, err)
		require.Equal(t, 0, total)
		// make([]*UserFactDetail, 0, len(facts))：空结果必须是空且非 nil，不能返回 nil 切片。
		require.NotNil(t, got)
		require.Empty(t, got)
		facts.AssertExpectations(t)
	})
}

func TestMemoryService_DeleteUserEntity(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes own entity", func(t *testing.T) {
		svc, _, _, entities, _ := newFactSvc()
		entities.On("GetByID", ctx, "tenant-1", "ent-1").
			Return(&domain.MemoryEntity{ID: "ent-1", UserID: "user-1"}, nil).Once()
		entities.On("Delete", ctx, "tenant-1", "ent-1").Return(nil).Once()
		require.NoError(t, svc.DeleteUserEntity(ctx, "tenant-1", "user-1", "ent-1"))
		entities.AssertExpectations(t)
	})

	t.Run("rejects other users entity as 404", func(t *testing.T) {
		svc, _, _, entities, _ := newFactSvc()
		entities.On("GetByID", ctx, "tenant-1", "ent-1").
			Return(&domain.MemoryEntity{ID: "ent-1", UserID: "other-user"}, nil).Once()
		err := svc.DeleteUserEntity(ctx, "tenant-1", "user-1", "ent-1")
		require.ErrorIs(t, err, domain.ErrEntityNotFound)
		entities.AssertExpectations(t)
	})
}

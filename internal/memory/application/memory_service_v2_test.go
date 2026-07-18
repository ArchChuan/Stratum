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

func TestMemoryService_CreateAndReadUserMemory(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)

	facts.On("Create", ctx, "tenant-1", mock.MatchedBy(func(f *domain.MemoryFact) bool {
		return f.TenantID == "tenant-1" && f.UserID == "user-1" && f.Scope == domain.ScopeUser &&
			f.Content == "prefers concise answers" && f.Importance == 0.8 &&
			f.Category == "other" && f.Confidence == 1.0 && f.Source == domain.FactSourceExplicitUser
	})).Return(nil).Once()

	created, err := svc.CreateUserMemory(ctx, &CreateUserMemoryRequest{
		TenantID: "tenant-1", UserID: "user-1", Content: "prefers concise answers", Importance: 0.8,
	})
	assert.NoError(t, err)
	assert.Equal(t, "prefers concise answers", created.Content)
	assert.Equal(t, "user", created.Scope)

	fact := &domain.MemoryFact{ID: created.ID, TenantID: "tenant-1", UserID: "user-1", Scope: domain.ScopeUser, Content: created.Content}
	facts.On("GetByID", ctx, "tenant-1", created.ID).Return(fact, nil).Once()
	read, err := svc.GetUserMemory(ctx, &GetUserMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: created.ID})
	assert.NoError(t, err)
	assert.Equal(t, created.ID, read.ID)
	facts.AssertExpectations(t)
}

func TestMemoryService_UserMemoryOwnership(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)
	fact := &domain.MemoryFact{ID: "fact-1", TenantID: "tenant-1", UserID: "other-user", Scope: domain.ScopeUser}

	facts.On("GetByID", ctx, "tenant-1", "fact-1").Return(fact, nil).Twice()
	_, err := svc.GetUserMemory(ctx, &GetUserMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: "fact-1"})
	assert.ErrorIs(t, err, domain.ErrScopeMismatch)
	err = svc.ForgetUserMemory(ctx, &ForgetMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: "fact-1"})
	assert.ErrorIs(t, err, domain.ErrScopeMismatch)
	facts.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything)
}

func TestMemoryService_UserEndpointRejectsAgentScopedFact(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)
	fact := &domain.MemoryFact{ID: "fact-1", TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", Scope: domain.ScopeAgent}

	facts.On("GetByID", ctx, "tenant-1", "fact-1").Return(fact, nil).Once()
	_, err := svc.GetUserMemory(ctx, &GetUserMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: "fact-1"})
	assert.ErrorIs(t, err, domain.ErrScopeMismatch)
}

func TestMemoryService_GetUserMemoryPreservesNotFound(t *testing.T) {
	ctx := context.Background()
	facts := new(MockFactRepo)
	svc := NewMemoryService(facts, nil, nil, nil, nil, nil, nil, nil)
	facts.On("GetByID", ctx, "tenant-1", "missing").Return(nil, domain.ErrFactNotFound).Once()

	_, err := svc.GetUserMemory(ctx, &GetUserMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: "missing"})
	assert.True(t, errors.Is(err, domain.ErrFactNotFound))
}

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

func (m *MockEntityRepo) FindByNameAndType(ctx context.Context, tenantID string, filter domain.ScopeFilter, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	args := m.Called(ctx, tenantID, filter, name, entityType, threshold)
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

func TestForgetUserMemoryVectorFailurePreservesFactForOwnershipRetry(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	vectorStore := new(MockVectorStore)
	svc := NewMemoryService(factRepo, nil, nil, vectorStore, nil, nil, nil, nil)
	req := &ForgetMemoryRequest{TenantID: "tenant-1", UserID: "user-1", FactID: "fact-1"}
	wantErr := errors.New("milvus unavailable")
	factRepo.On("GetByID", ctx, req.TenantID, req.FactID).Return(&domain.MemoryFact{
		ID: req.FactID, UserID: req.UserID, Scope: domain.ScopeUser,
	}, nil).Once()
	vectorStore.On("Delete", ctx, "memory_facts_tenant_1", []string{req.FactID}).Return(wantErr).Once()

	err := svc.ForgetUserMemory(ctx, req)

	assert.ErrorIs(t, err, wantErr)
	assert.ErrorContains(t, err, "forget memory vector replica")
	factRepo.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything)
}

func TestForgetMemoryFactFailureAfterVectorSuccessRemainsRetryable(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	vectorStore := new(MockVectorStore)
	svc := NewMemoryService(factRepo, nil, nil, vectorStore, nil, nil, nil, nil)
	req := &ForgetMemoryRequest{TenantID: "tenant-1", FactID: "fact-1"}
	wantErr := errors.New("postgres unavailable")
	vectorStore.On("Delete", ctx, "memory_facts_tenant_1", []string{req.FactID}).Return(nil).Twice()
	factRepo.On("Delete", ctx, req.TenantID, req.FactID).Return(wantErr).Once()
	factRepo.On("Delete", ctx, req.TenantID, req.FactID).Return(nil).Once()

	assert.ErrorIs(t, svc.ForgetMemory(ctx, req), wantErr)
	assert.NoError(t, svc.ForgetMemory(ctx, req))
	factRepo.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
}

func TestForgetMemoryWithoutVectorStoreDeletesFact(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	svc := NewMemoryService(factRepo, nil, nil, nil, nil, nil, nil, nil)
	req := &ForgetMemoryRequest{TenantID: "tenant-1", FactID: "fact-1"}
	factRepo.On("Delete", ctx, req.TenantID, req.FactID).Return(nil).Once()

	assert.NoError(t, svc.ForgetMemory(ctx, req))
	factRepo.AssertExpectations(t)
}

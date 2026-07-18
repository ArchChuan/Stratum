package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func sourceExtractionService(t *testing.T, facts []*port.ExtractedFact) (*MemoryService, *MockFactRepo, *MockEntityRepo, *MockVectorStore, *MockEmbedClient) {
	t.Helper()
	factRepo := new(MockFactRepo)
	factRepo.On("FindSupersedeCandidates", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*port.SupersedeCandidate{}, nil)
	entityRepo := new(MockEntityRepo)
	vectorStore := new(MockVectorStore)
	embedClient := new(MockEmbedClient)
	extractor := new(MockLLMExtractor)
	extractor.On("ExtractFacts", mock.Anything, "user-1", "agent-1", mock.Anything).Return(facts, nil)
	svc := NewMemoryService(factRepo, entityRepo, new(MockExtractionQueue), vectorStore, extractor, embedClient, nil, nil)
	return svc, factRepo, entityRepo, vectorStore, embedClient
}

func sourceExtractRequest() *ExtractFactsRequest {
	return &ExtractFactsRequest{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", Scope: "user",
		SourceMessageID: "message-1", SourceTaskID: 42,
		Messages: []MessageDTO{{Role: "user", Content: "I use Go"}},
	}
}

func TestExtractFactsSourceReplayReusesPersistedIDAndRetriesVector(t *testing.T) {
	facts := []*port.ExtractedFact{{Content: "User uses Go", Importance: 0.8, FactType: "skill", Entities: []string{"Go"}}}
	svc, factRepo, entityRepo, vectors, embed := sourceExtractionService(t, facts)
	persisted, err := domain.NewFactWithMeta("tenant-1", "user-1", "agent-1", "", "user", "User uses Go", 0.8, 0.8, "skill", domain.FactSourceLLMExtraction, []string{"Go"})
	require.NoError(t, err)
	persisted.ID = "11111111-1111-4111-8111-111111111111"
	persisted.EntityIDs = []string{"entity-1"}

	factRepo.On("CreateExtracted", mock.Anything, "tenant-1", mock.MatchedBy(func(write *port.ExtractedFactWrite) bool {
		return write.Identity.MessageID == "message-1" && write.Identity.TaskID == 42 && write.Identity.Ordinal == 0 && write.PayloadHash != ""
	})).Return(persisted, true, nil).Once()
	factRepo.On("CreateExtracted", mock.Anything, "tenant-1", mock.Anything).Return(persisted, false, nil).Once()
	embed.On("Embed", mock.Anything, "User uses Go").Return([]float32{0.1, 0.2}, nil).Twice()
	var vectorIDs []string
	vectors.On("Upsert", mock.Anything, "memory_facts_tenant_1", mock.Anything).Run(func(args mock.Arguments) {
		docs := args.Get(2).([]*port.VectorDoc)
		vectorIDs = append(vectorIDs, docs[0].ID)
	}).Return(errors.New("milvus unavailable")).Once()
	vectors.On("Upsert", mock.Anything, "memory_facts_tenant_1", mock.Anything).Run(func(args mock.Arguments) {
		docs := args.Get(2).([]*port.VectorDoc)
		vectorIDs = append(vectorIDs, docs[0].ID)
	}).Return(nil).Once()

	require.ErrorContains(t, svc.ExtractFacts(context.Background(), sourceExtractRequest()), "upsert vector")
	require.NoError(t, svc.ExtractFacts(context.Background(), sourceExtractRequest()))
	require.Equal(t, []string{persisted.ID, persisted.ID}, vectorIDs)
	entityRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	entityRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtractFactsSourceConflictStopsBeforeEntityAndVectorMutation(t *testing.T) {
	svc, factRepo, entityRepo, vectors, embed := sourceExtractionService(t, []*port.ExtractedFact{{Content: "changed", Importance: 0.8, Entities: []string{"Go"}}})
	factRepo.On("CreateExtracted", mock.Anything, "tenant-1", mock.Anything).Return(nil, false, domain.ErrFactSourceConflict).Once()

	err := svc.ExtractFacts(context.Background(), sourceExtractRequest())
	require.ErrorIs(t, err, domain.ErrFactSourceConflict)
	entityRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	entityRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
	vectors.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything, mock.Anything)
	embed.AssertNotCalled(t, "Embed", mock.Anything, mock.Anything)
}

func TestExtractFactsUsesOriginalOrdinalAfterConfidenceGate(t *testing.T) {
	low, high := 0.1, 0.9
	facts := []*port.ExtractedFact{
		{Content: "filtered", Importance: 0.9, Confidence: &low},
		{Content: "survives", Importance: 0.8, Confidence: &high},
	}
	svc, factRepo, _, vectors, embed := sourceExtractionService(t, facts)
	persisted, err := domain.NewFact("tenant-1", "user-1", "agent-1", "", "user", "survives", 0.8, nil)
	require.NoError(t, err)
	factRepo.On("CreateExtracted", mock.Anything, "tenant-1", mock.MatchedBy(func(write *port.ExtractedFactWrite) bool {
		return write.Identity.Ordinal == 1
	})).Return(persisted, true, nil).Once()
	embed.On("Embed", mock.Anything, "survives").Return([]float32{0.1}, nil).Once()
	vectors.On("Upsert", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	require.NoError(t, svc.ExtractFacts(context.Background(), sourceExtractRequest()))
}

func TestExtractFactsRejectsPartialSourceIdentityInsteadOfUsingLegacyPath(t *testing.T) {
	svc, _, entityRepo, vectors, embed := sourceExtractionService(t, []*port.ExtractedFact{{Content: "fact", Importance: 0.8}})
	req := sourceExtractRequest()
	req.SourceMessageID = ""

	err := svc.ExtractFacts(context.Background(), req)
	require.ErrorIs(t, err, domain.ErrInvalidFactSourceIdentity)
	entityRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	vectors.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything, mock.Anything)
	embed.AssertNotCalled(t, "Embed", mock.Anything, mock.Anything)
}

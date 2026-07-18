package application

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// TestExtractFacts_LowConfidenceFilter 低置信度的事实在 constants.FactConfidenceMin 以下不得持久化
func TestExtractFacts_LowConfidenceFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extraction quality test")
	}
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil, nil)

	lowConf := float64(0.1) // below FactConfidenceMin (0.3) → 应被过滤
	highConf := float64(0.8)

	llmExtract.On("ExtractFacts", ctx, "user1", "agent1", mock.Anything).Return([]*port.ExtractedFact{
		{Content: "Low confidence fact", Importance: 0.5, Confidence: &lowConf, FactType: "other", Entities: []string{}},
		{Content: "High confidence fact", Importance: 0.7, Confidence: &highConf, FactType: "skill", Entities: []string{}},
	}, nil)

	// 只有高置信度的事实有 supersede/entity/create/embed 调用
	factRepo.On("FindSupersedeCandidates", ctx, "t1", mock.Anything,
		"High confidence fact", mock.Anything, mock.Anything).
		Return([]*port.SupersedeCandidate{}, nil)
	entityRepo.On("FindByNameAndType", ctx, "t1", mock.Anything, mock.Anything, "", mock.Anything).
		Return(nil, domain.ErrEntityNotFound).Maybe()
	entityRepo.On("Create", ctx, "t1", mock.Anything).Return(nil).Maybe()

	factRepo.On("Create", ctx, "t1", mock.MatchedBy(func(f *memFactType) bool {
		return f.Content == "High confidence fact" &&
			f.ConversationID == "conv1" &&
			f.Category == "skill" &&
			f.Confidence == highConf &&
			f.Source == domain.FactSourceLLMExtraction
	})).Return(nil)

	embedClient.On("Embed", ctx, "High confidence fact").Return([]float32{0.1}, nil)
	vectorStore.On("Upsert", ctx, mock.Anything, mock.MatchedBy(func(docs []*port.VectorDoc) bool {
		if len(docs) != 1 {
			return false
		}
		metadata := docs[0].Metadata
		confidence, ok := metadata["confidence"].(float64)
		return metadata["category"] == "skill" && ok && confidence == highConf &&
			metadata["source"] == domain.FactSourceLLMExtraction &&
			metadata["conversation_id"] == "conv1" && metadata["scope"] == "user"
	})).Return(nil)

	req := &ExtractFactsRequest{
		TenantID: "t1", UserID: "user1", AgentID: "agent1", Scope: "user",
		ConversationID: "conv1",
		Messages:       []MessageDTO{{Role: "user", Content: "content"}},
	}
	err := svc.ExtractFacts(ctx, req)
	assert.NoError(t, err)

	// 验证低置信度事实绝不被创建
	for _, call := range factRepo.Calls {
		if call.Method == "Create" {
			if f, ok := call.Arguments.Get(2).(*memFactType); ok {
				assert.NotEqual(t, "Low confidence fact", f.Content,
					"低置信度事实不应被持久化")
			}
		}
	}

	// 高置信度事实必须被创建
	factRepo.AssertCalled(t, "Create", ctx, "t1", mock.MatchedBy(func(f *memFactType) bool {
		return f.Content == "High confidence fact"
	}))
}

// TestExtractFacts_QualitySort_PerRoundLimit 超过 FactPerRoundPersistLimit 时按质量排序取前 N
func TestExtractFacts_QualitySort_PerRoundLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extraction quality sort test")
	}

	// 验证常量值合理
	if constants.FactPerRoundPersistLimit <= 0 {
		t.Fatalf("FactPerRoundPersistLimit must be > 0, got %d", constants.FactPerRoundPersistLimit)
	}

	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)
	vectorStore := new(MockVectorStore)
	llmExtract := new(MockLLMExtractor)
	embedClient := new(MockEmbedClient)

	svc := NewMemoryService(factRepo, entityRepo, queue, vectorStore, llmExtract, embedClient, nil, nil)

	// 生成 FactPerRoundPersistLimit+2 个事实（全部超过置信阈值）
	limit := constants.FactPerRoundPersistLimit
	facts := make([]*port.ExtractedFact, limit+2)
	for i := range facts {
		conf := 0.4 + float64(i)*0.02 // 都 >= FactConfidenceMin
		c := conf
		facts[i] = &port.ExtractedFact{
			Content:    fmt.Sprintf("fact-%02d", i),
			Importance: 0.5,
			Confidence: &c,
			FactType:   "other",
			Entities:   []string{},
		}
	}

	llmExtract.On("ExtractFacts", ctx, "user1", "agent1", mock.Anything).Return(facts, nil)

	// 为所有事实设置 mock（只有前 limit 个会被实际调用）
	factRepo.On("FindSupersedeCandidates", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Return([]*port.SupersedeCandidate{}, nil)
	factRepo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	embedClient.On("Embed", mock.Anything, mock.Anything).Return([]float32{0.1}, nil)
	vectorStore.On("Upsert", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	entityRepo.On("FindByNameAndType", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, domain.ErrEntityNotFound).Maybe()
	entityRepo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	req := &ExtractFactsRequest{
		TenantID: "t1", UserID: "user1", AgentID: "agent1", Scope: "user",
		ConversationID: "conv1",
		Messages:       []MessageDTO{{Role: "user", Content: "content"}},
	}
	err := svc.ExtractFacts(ctx, req)
	assert.NoError(t, err)

	var created []string
	for _, call := range factRepo.Calls {
		if call.Method == "Create" {
			created = append(created, call.Arguments.Get(2).(*domain.MemoryFact).Content)
		}
	}
	assert.Len(t, created, limit)
	for i, content := range created {
		assert.Equal(t, fmt.Sprintf("fact-%02d", len(facts)-1-i), content)
	}
}

// memFactType 是 *domain.MemoryFact 的别名，供 mock.MatchedBy 使用
type memFactType = domain.MemoryFact

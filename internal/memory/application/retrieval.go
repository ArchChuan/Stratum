package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
)

var ErrInvalidRecallMemoryRequest = errors.New("invalid recall memory request")

type scoredFact struct {
	fact  *domain.MemoryFact
	score float64
}

// RecallMemory performs hybrid retrieval: vector search + trigram search + RRF fusion.
func (s *MemoryService) RecallMemory(ctx context.Context, req *RecallMemoryRequest) (*RecallMemoryResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidRecallMemoryRequest)
	}
	query := strings.TrimSpace(req.Query)
	if req.TenantID == "" || req.UserID == "" || query == "" || req.TopK <= 0 {
		return nil, fmt.Errorf("%w: tenant ID, user ID, query, and positive top K are required", ErrInvalidRecallMemoryRequest)
	}
	// Step 1: Embed query for vector search
	queryVector, err := s.embedClient.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Step 2: Vector search (retrieve 2*topK candidates) — 新名 collection 优先，
	// 不存在（升级后未重建）时回退 legacy 名；无可用模型时直接 legacy 名。
	model := s.currentEmbedModel(ctx, req.TenantID)
	collections := []string{factsCollectionName(req.TenantID, model)}
	if model != "" {
		collections = append(collections, fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(req.TenantID, "-", "_")))
	}
	filter := port.VectorSearchFilter{
		UserID: req.UserID, AgentID: req.AgentID, IncludeUserScope: true, IncludeAgentScope: req.AgentID != "",
	}

	vectorDocs, err := s.vectorSearchCandidates(ctx, collections, queryVector, req.TopK*2, filter)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Step 3: Trigram search (retrieve 2*topK candidates)
	scopeFilter := domain.BuildScopeFilter(req.TenantID, req.UserID, req.AgentID, "user")
	trigramFacts, err := s.factRepo.SearchByContent(ctx, req.TenantID, scopeFilter, query, req.TopK*2)
	if err != nil {
		return nil, fmt.Errorf("trigram search: %w", err)
	}

	// Step 4: Build rank maps
	vectorRanks := make(map[string]int)
	for i, doc := range vectorDocs {
		vectorRanks[doc.ID] = i + 1 // rank starts at 1
	}

	trigramRanks := make(map[string]int)
	for i, fact := range trigramFacts {
		trigramRanks[fact.ID] = i + 1
	}

	// Step 5: Collect all unique fact IDs
	allIDs := make(map[string]bool)
	for _, doc := range vectorDocs {
		allIDs[doc.ID] = true
	}
	for _, fact := range trigramFacts {
		allIDs[fact.ID] = true
	}

	// Step 6: Calculate RRF score for each fact
	k := float64(constants.MemoryRRFConstant)
	var scored []scoredFact

	for id := range allIDs {
		vectorRank := vectorRanks[id]
		trigramRank := trigramRanks[id]

		// RRF formula: score = 1/(k+rank_vector) + 1/(k+rank_trigram)
		rrfScore := 0.0
		if vectorRank > 0 {
			rrfScore += 1.0 / (k + float64(vectorRank))
		}
		if trigramRank > 0 {
			rrfScore += 1.0 / (k + float64(trigramRank))
		}

		// Fetch full fact
		fact, err := s.factRepo.GetByID(ctx, req.TenantID, id)
		if err != nil {
			continue // skip if not found
		}

		scored = append(scored, scoredFact{fact: fact, score: rrfScore})
	}

	// Step 7: Sort by RRF score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Step 8: Take top-K and increment access_count
	topK := req.TopK
	if topK > len(scored) {
		topK = len(scored)
	}

	var dtos []FactDTO
	for i := 0; i < topK; i++ {
		fact := scored[i].fact

		// Increment access count and refresh frecency score (best-effort, don't fail recall on update error)
		fact.AccessCount++
		fact.LastAccessAt = timeutil.Now()
		daysSince := timeutil.Now().Sub(fact.CreatedAt).Hours() / 24
		fact.FrecencyScore = domain.CalculateFrecency(fact.Importance, daysSince, fact.AccessCount)
		_ = s.factRepo.Update(ctx, req.TenantID, fact)

		dtos = append(dtos, FactDTO{
			ID:          fact.ID,
			Content:     fact.Content,
			Importance:  fact.Importance,
			Keywords:    nil, // keywords not stored in current schema
			EntityNames: fact.EntityNames,
			AccessCount: fact.AccessCount,
			CreatedAt:   fact.CreatedAt,
		})
	}

	return &RecallMemoryResponse{Facts: dtos}, nil
}

// errCollectionNotFound distinguishes a missing Milvus collection from other
// search failures so vectorSearchCandidates can decide between legacy fallback
// and fail-closed error propagation.
var errCollectionNotFound = errors.New("memory collection not found")

// isCollectionNotFound mirrors knowledge/application 的判定形状：本地哨兵或错误
// 文本包含 pkg/storage/milvus.ErrCollectionNotFound（"milvus collection not
// found"）的消息片段（errors.Is 哨兵在 infrastructure 层直接判定）。
func isCollectionNotFound(err error) bool {
	return errors.Is(err, errCollectionNotFound) ||
		strings.Contains(err.Error(), "collection not found")
}

// vectorSearchCandidates 双名兜底：按 [新名, legacy] 顺序查询并合并结果。
// 仅 collection-not-found 容忍（继续尝试下一个，legacy 回退）；其余错误
// （schema 不匹配、Milvus 不可用）向上传播，保持 fail-closed——向量库故障
// 必须显式失败，不得静默退化为 trigram 检索。
func (s *MemoryService) vectorSearchCandidates(ctx context.Context, collections []string, queryVector []float32, topK int, filter port.VectorSearchFilter) ([]*port.VectorDoc, error) {
	var vectorDocs []*port.VectorDoc
	for _, collectionName := range collections {
		if collectionName == "" {
			continue
		}
		docs, err := s.vectorStore.Search(ctx, collectionName, queryVector, topK, filter)
		if err != nil {
			if isCollectionNotFound(err) {
				continue // collection 不存在 → 尝试下一个（legacy 回退）
			}
			return nil, err
		}
		vectorDocs = append(vectorDocs, docs...)
	}
	return vectorDocs, nil
}

package application

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// recordingVectorStore wraps MockVectorStore and records the DescribeCollection
// and Search call sequences, so the G7 fallback state machine can be asserted
// directly: which collection names were probed, which were searched, in what
// order. Per-collection overrides fall through to MockVectorStore when absent.
type recordingVectorStore struct {
	*MockVectorStore
	describeCalls []string
	searchCalls   []string
	describeInfo  map[string]knowledgeport.CollectionInfo
	describeErr   map[string]error
	searchOut     map[string][]knowledgeport.VectorSearchResult
	searchFail    map[string]error
}

func (r *recordingVectorStore) DescribeCollection(ctx context.Context, name string) (knowledgeport.CollectionInfo, error) {
	r.describeCalls = append(r.describeCalls, name)
	if err, ok := r.describeErr[name]; ok {
		return knowledgeport.CollectionInfo{}, err
	}
	if info, ok := r.describeInfo[name]; ok {
		return info, nil
	}
	return r.MockVectorStore.DescribeCollection(ctx, name)
}

func (r *recordingVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int) ([]knowledgeport.VectorSearchResult, error) {
	r.searchCalls = append(r.searchCalls, collection)
	if err, ok := r.searchFail[collection]; ok {
		return nil, err
	}
	if out, ok := r.searchOut[collection]; ok {
		return out, nil
	}
	return r.MockVectorStore.Search(ctx, collection, vector, topK)
}

// SearchWithFilter records the same call sequence as Search — the RAG legs
// search through SearchWithFilter after the viewer-whitelist fusion — so the
// G7 fallback state machine assertions hold for both search paths.
func (r *recordingVectorStore) SearchWithFilter(ctx context.Context, collection string, vector []float32, topK int, expression string) ([]knowledgeport.VectorSearchResult, error) {
	r.searchCalls = append(r.searchCalls, collection)
	if err, ok := r.searchFail[collection]; ok {
		return nil, err
	}
	if out, ok := r.searchOut[collection]; ok {
		return out, nil
	}
	return r.MockVectorStore.SearchWithFilter(ctx, collection, vector, topK, expression)
}

// G7 状态机测试：kb_<workspace>_<model> 命名 + legacy 回退先于 drift 分类。
// newName 是带模型后缀的新集合名，legacyName 是升级前无后缀的存量名；
// 当前模型 text-embedding-v3 → 1024 维。

const (
	fallbackTenant  = "t1"
	fallbackWSID    = "wsid-1"
	fallbackModel   = "text-embedding-v3"
	fallbackNewName = "kb_wsid_1_text_embedding_v3"
	fallbackLegacy  = "kb_wsid_1"
)

func fallbackQueryReq() RAGQueryRequest {
	return RAGQueryRequest{
		Question: "q", TenantID: fallbackTenant, WorkspaceID: fallbackWSID,
		EmbeddingModel: fallbackModel, Mode: "vector",
		// D2 gate requires a viewer identity; wsRepo is unset in these tests,
		// so visibility resolves unrestricted and the G7 fallback state machine
		// is exercised without doc-level filtering.
		ViewerID: "fallback-viewer",
	}
}

func newFallbackRAGService(rec *recordingVectorStore) *RAGService {
	return NewRAGService(&mockEmbedder{dim: 4}, rec, zap.NewNop())
}

func TestG7LegacyFallback_UpgradedWorkspaceReadsLegacyCollection(t *testing.T) {
	// 升级态（未 re-ingest）：新名缺失是预期状态 → 回退 legacy 名读取旧数据。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeErr:     map[string]error{fallbackNewName: errors.New("collection not found: " + fallbackNewName)},
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackLegacy: {Dim: 1024, HasUserID: true}},
		searchOut:       map[string][]knowledgeport.VectorSearchResult{fallbackLegacy: {{Content: "legacy-data"}}},
	}
	rs := newFallbackRAGService(rec)

	res, err := rs.Query(context.Background(), fallbackQueryReq())
	if err != nil {
		t.Fatalf("query = %v", err)
	}
	if len(res.VectorResults) != 1 || res.VectorResults[0].Content != "legacy-data" {
		t.Fatalf("results = %+v", res.VectorResults)
	}
	// 断言调用序列：先探测新名（not found）→ 探测/校验 legacy → 只搜 legacy。
	if got := rec.searchCalls; len(got) != 1 || got[0] != fallbackLegacy {
		t.Fatalf("search calls = %v, want [%s]", got, fallbackLegacy)
	}
	if got := rec.describeCalls; len(got) != 2 || got[0] != fallbackNewName || got[1] != fallbackLegacy {
		t.Fatalf("describe calls = %v, want [%s %s]", got, fallbackNewName, fallbackLegacy)
	}
}

func TestG7LegacyFallback_ModelSwitchedUsesOnlyNewName(t *testing.T) {
	// 换模型态（已 re-ingest）：新名存在 → 只用新名，绝不去摸 legacy。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackNewName: {Dim: 1024, HasUserID: true}},
		searchOut:       map[string][]knowledgeport.VectorSearchResult{fallbackNewName: {{Content: "fresh-data"}}},
	}
	rs := newFallbackRAGService(rec)

	res, err := rs.Query(context.Background(), fallbackQueryReq())
	if err != nil {
		t.Fatalf("query = %v", err)
	}
	if len(res.VectorResults) != 1 || res.VectorResults[0].Content != "fresh-data" {
		t.Fatalf("results = %+v", res.VectorResults)
	}
	if got := rec.searchCalls; len(got) != 1 || got[0] != fallbackNewName {
		t.Fatalf("search calls = %v, want [%s]", got, fallbackNewName)
	}
	for _, call := range rec.describeCalls {
		if call == fallbackLegacy {
			t.Fatalf("legacy collection must not be probed when new name exists: %v", rec.describeCalls)
		}
	}
}

func TestG7LegacyFallback_LegacyDimMismatchSkipsNotFails(t *testing.T) {
	// legacy 集合维数与当前模型不符（旧模型写入的存量数据）：Warn + 空结果，
	// 不 fail-closed（spec：legacy dim 不一致不报错，也不误判 drift）。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeErr:     map[string]error{fallbackNewName: errors.New("collection not found: " + fallbackNewName)},
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackLegacy: {Dim: 1536, HasUserID: true}},
	}
	rs := newFallbackRAGService(rec)

	res, err := rs.Query(context.Background(), fallbackQueryReq())
	if err != nil {
		t.Fatalf("query = %v, want skip not fail", err)
	}
	if len(res.VectorResults) != 0 {
		t.Fatalf("results = %+v, want empty", res.VectorResults)
	}
	// dim 不一致在搜索前拦截：一次 Search 都不该发生。
	if len(rec.searchCalls) != 0 {
		t.Fatalf("search calls = %v, want none", rec.searchCalls)
	}
}

func TestG7LegacyFallback_NotReIngestedDoesNotMisjudgeDrift(t *testing.T) {
	// 未 re-ingest 时 PG 中还有旧 chunks：legacy 回退成功读取 → 不得因新名缺失
	// 误判 drift 而 ErrRAGDependency（count>0 时 drift 路径会 fail-closed）。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeErr:     map[string]error{fallbackNewName: errors.New("collection not found: " + fallbackNewName)},
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackLegacy: {Dim: 1024, HasUserID: true}},
		searchOut:       map[string][]knowledgeport.VectorSearchResult{fallbackLegacy: {{Content: "legacy-data"}}},
	}
	rs := newFallbackRAGService(rec)
	rs.SetChunkRepo(&mockChunkRepo{countByWorkspace: 42})

	res, err := rs.Query(context.Background(), fallbackQueryReq())
	if err != nil {
		t.Fatalf("legacy fallback must not trigger drift: %v", err)
	}
	if len(res.VectorResults) != 1 {
		t.Fatalf("results = %+v", res.VectorResults)
	}
}

func TestG7LegacyFallback_NewNameDimMismatchFailsClosed(t *testing.T) {
	// 新名存在但维数与当前模型不符（换模型后旧维度数据没清理）：仍 fail-closed，
	// 不允许用错误维度数据回答。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackNewName: {Dim: 512, HasUserID: true}},
	}
	rs := newFallbackRAGService(rec)

	_, err := rs.Query(context.Background(), fallbackQueryReq())
	if !errors.Is(err, ErrRAGDependency) {
		t.Fatalf("err = %v, want ErrRAGDependency", err)
	}
	if len(rec.searchCalls) != 0 {
		t.Fatalf("search calls = %v, want none", rec.searchCalls)
	}
}

func hybridFallbackQueryReq() RAGQueryRequest {
	req := fallbackQueryReq()
	req.Mode = "hybrid"
	return req
}

func TestG7Hybrid_UpgradedWorkspaceFallsBackToLegacy(t *testing.T) {
	// 升级态 workspace（仅 legacy collection 存在）走 hybrid：新名缺失回退
	// legacy 名，vector leg 读旧数据，keyword leg 照常融合返回。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeErr:     map[string]error{fallbackNewName: errors.New("collection not found: " + fallbackNewName)},
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackLegacy: {Dim: 1024, HasUserID: true}},
		searchOut:       map[string][]knowledgeport.VectorSearchResult{fallbackLegacy: {{ID: "v1", Content: "legacy-data"}}},
	}
	rs := newFallbackRAGService(rec)
	rs.SetChunkRepo(&mockChunkRepo{keywordOut: []domain.Chunk{{ID: "c1", DocID: "d1", Text: "kw-data"}}})

	res, err := rs.Query(context.Background(), hybridFallbackQueryReq())
	if err != nil {
		t.Fatalf("query = %v", err)
	}
	// 真实调用序列断言：[新名 Describe, legacy Describe, legacy Search]，
	// 与 vector 分支回退路径同构。
	if got := rec.describeCalls; len(got) != 2 || got[0] != fallbackNewName || got[1] != fallbackLegacy {
		t.Fatalf("describe calls = %v, want [%s %s]", got, fallbackNewName, fallbackLegacy)
	}
	if got := rec.searchCalls; len(got) != 1 || got[0] != fallbackLegacy {
		t.Fatalf("search calls = %v, want [%s]", got, fallbackLegacy)
	}
	// 两腿结果都进 fusion，互不吞没。
	var hasLegacy, hasKeyword bool
	for _, s := range res.Sources {
		hasLegacy = hasLegacy || s.Content == "legacy-data"
		hasKeyword = hasKeyword || s.Content == "kw-data"
	}
	if !hasLegacy || !hasKeyword {
		t.Fatalf("sources = %+v, want both legacy-data and kw-data", res.Sources)
	}
}

func TestG7Hybrid_LegacyDimMismatchSkipsVectorKeepsKeyword(t *testing.T) {
	// hybrid 中 legacy 集合维数与当前模型不符：vector leg 空结果（Warn 已
	// 记录），不 fail hybrid —— keyword leg 结果照常返回。
	rec := &recordingVectorStore{
		MockVectorStore: NewMockVectorStore(),
		describeErr:     map[string]error{fallbackNewName: errors.New("collection not found: " + fallbackNewName)},
		describeInfo:    map[string]knowledgeport.CollectionInfo{fallbackLegacy: {Dim: 1536, HasUserID: true}},
	}
	rs := newFallbackRAGService(rec)
	rs.SetChunkRepo(&mockChunkRepo{keywordOut: []domain.Chunk{{ID: "c1", DocID: "d1", Text: "kw-data"}}})

	res, err := rs.Query(context.Background(), hybridFallbackQueryReq())
	if err != nil {
		t.Fatalf("query = %v, want keyword leg only, not fail", err)
	}
	if len(res.VectorResults) != 0 {
		t.Fatalf("vector results = %+v, want empty", res.VectorResults)
	}
	// dim 不一致在搜索前拦截：vector leg 一次 Search 都不该发生。
	if len(rec.searchCalls) != 0 {
		t.Fatalf("search calls = %v, want none", rec.searchCalls)
	}
	if len(res.Sources) == 0 || res.Sources[0].Content != "kw-data" {
		t.Fatalf("sources = %+v, want keyword leg result", res.Sources)
	}
}

func TestG7LegacyFallback_BothMissingClassifiesDrift(t *testing.T) {
	// 新名与 legacy 都缺失 → 走 handleMissingCollection 分类：
	// PG 无 chunks = 合法空 workspace（空结果）；PG 有 chunks = drift 且 fail-closed。
	newErr := errors.New("collection not found: " + fallbackNewName)
	for _, tc := range []struct {
		name       string
		chunkCount int64
		wantErr    bool
	}{
		{name: "no chunks is legitimately empty", chunkCount: 0, wantErr: false},
		{name: "chunks present is drift", chunkCount: 3, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingVectorStore{
				MockVectorStore: NewMockVectorStore(),
				describeErr:     map[string]error{fallbackNewName: newErr, fallbackLegacy: newErr},
			}
			rs := newFallbackRAGService(rec)
			rs.SetChunkRepo(&mockChunkRepo{countByWorkspace: tc.chunkCount})

			_, err := rs.Query(context.Background(), fallbackQueryReq())
			if tc.wantErr && !errors.Is(err, ErrRAGDependency) {
				t.Fatalf("err = %v, want ErrRAGDependency", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want empty result", err)
			}
		})
	}
}

package pipeline

import (
	"context"
	"errors"
	"testing"

	vector "github.com/byteBuilderX/stratum/pkg/vector"
	"go.uber.org/zap"
)

// fakeEmbedClient returns a fixed vector so tryVectorSearch reaches the store.
type fakeEmbedClient struct {
	vec   []float32
	err   error
	model string
}

func (f *fakeEmbedClient) EmbedVector(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}
func (f *fakeEmbedClient) GetVectorDimension() int { return len(f.vec) }
func (f *fakeEmbedClient) Model() string           { return f.model }

// fakeVectorSearcher records the collections queried and returns per-collection
// canned results/errors so we can assert the dual-collection fusion behaviour.
type fakeVectorSearcher struct {
	byCollection map[string][]vector.SearchResult
	errByColl    map[string]error
	queried      []string
}

func (f *fakeVectorSearcher) SearchWithFilter(_ context.Context, collectionName string, _ []float32, _ int, _ string, _ ...string) ([]vector.SearchResult, error) {
	f.queried = append(f.queried, collectionName)
	if err := f.errByColl[collectionName]; err != nil {
		return nil, err
	}
	return f.byCollection[collectionName], nil
}

func newTestRecallHandler(embed EmbedClient, vs vectorSearcher) *RecallHandler {
	return &RecallHandler{logger: zap.NewNop(), embedSvc: embed, vectorDB: vs}
}

func TestTryVectorSearch_QueriesFourCandidatesInOrderAndSortsByDistance(t *testing.T) {
	tenant := "acme"
	model := "embedding-3"
	names := []string{
		memoryCollectionName(tenant, model),
		memoryCollectionLegacyName(tenant),
		memoryFactsCollectionName(tenant, model),
		memoryFactsCollectionLegacyName(tenant),
	}

	vs := &fakeVectorSearcher{byCollection: map[string][]vector.SearchResult{
		names[0]: {{ID: "raw-far", Content: "raw far", Score: 0.9}},
		names[2]: {{ID: "fact-near", Content: "fact near", Score: 0.1}},
	}}
	h := newTestRecallHandler(&fakeEmbedClient{vec: []float32{1, 2, 3}, model: model}, vs)

	got := h.tryVectorSearch(context.Background(), tenant, "u1", "", "user", RecallRequest{Query: "q", Limit: 5})

	// 候选 = 当前模型新名（raw+facts）∪ legacy 名（升级前数据），顺序固定。
	if len(vs.queried) != 4 || vs.queried[0] != names[0] || vs.queried[1] != names[1] || vs.queried[2] != names[2] || vs.queried[3] != names[3] {
		t.Fatalf("expected query sequence %v, got %v", names, vs.queried)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 merged candidates, got %d", len(got))
	}
	// Smaller L2 distance ranks first, regardless of source collection.
	if got[0].ID != "fact-near" || got[1].ID != "raw-far" {
		t.Fatalf("expected ascending-distance order [fact-near raw-far], got [%s %s]", got[0].ID, got[1].ID)
	}
}

func TestTryVectorSearch_LegacyOnlyWhenNoModel(t *testing.T) {
	tenant := "acme"

	vs := &fakeVectorSearcher{byCollection: map[string][]vector.SearchResult{
		memoryCollectionLegacyName(tenant):      {{ID: "legacy-raw", Content: "legacy raw", Score: 0.3}},
		memoryFactsCollectionLegacyName(tenant): {{ID: "legacy-fact", Content: "legacy fact", Score: 0.1}},
	}}
	h := newTestRecallHandler(&fakeEmbedClient{vec: []float32{1, 2, 3}, model: ""}, vs)

	got := h.tryVectorSearch(context.Background(), tenant, "u1", "", "user", RecallRequest{Query: "q", Limit: 5})

	// 无模型 → 只查 legacy 两个 collection（新名带尾下划线无意义，且升级前数据在旧名）。
	want := []string{memoryCollectionLegacyName(tenant), memoryFactsCollectionLegacyName(tenant)}
	if len(vs.queried) != 2 || vs.queried[0] != want[0] || vs.queried[1] != want[1] {
		t.Fatalf("expected legacy-only query sequence %v, got %v", want, vs.queried)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 merged legacy candidates, got %d", len(got))
	}
}

func TestTryVectorSearch_SkipsEmptyContentAndToleratesOneCollectionFailure(t *testing.T) {
	tenant := "acme"
	raw := memoryCollectionLegacyName(tenant)
	facts := memoryFactsCollectionLegacyName(tenant)

	vs := &fakeVectorSearcher{
		byCollection: map[string][]vector.SearchResult{
			raw: {
				{ID: "keep", Content: "has content", Score: 0.2},
				{ID: "drop", Content: "", Score: 0.1}, // empty content must be filtered out
			},
		},
		errByColl: map[string]error{facts: errors.New("facts collection down")},
	}
	h := newTestRecallHandler(&fakeEmbedClient{vec: []float32{1}}, vs)

	got := h.tryVectorSearch(context.Background(), tenant, "u1", "", "user", RecallRequest{Query: "q", Limit: 5})

	// A single failing collection must not abort the whole vector search.
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate (empty-content dropped, facts failure tolerated), got %d", len(got))
	}
	if got[0].ID != "keep" {
		t.Fatalf("expected surviving candidate 'keep', got %q", got[0].ID)
	}
}

func TestTryVectorSearch_NilVectorDBReturnsNil(t *testing.T) {
	h := newTestRecallHandler(&fakeEmbedClient{vec: []float32{1}}, nil)
	if got := h.tryVectorSearch(context.Background(), "t", "u", "", "user", RecallRequest{Query: "q", Limit: 5}); got != nil {
		t.Fatalf("expected nil when vectorDB absent, got %v", got)
	}
}

func TestTryVectorSearch_EmbedFailureReturnsNil(t *testing.T) {
	vs := &fakeVectorSearcher{}
	h := newTestRecallHandler(&fakeEmbedClient{err: errors.New("embed down")}, vs)
	if got := h.tryVectorSearch(context.Background(), "t", "u", "", "user", RecallRequest{Query: "q", Limit: 5}); got != nil {
		t.Fatalf("expected nil on embed failure, got %v", got)
	}
	if len(vs.queried) != 0 {
		t.Fatal("vector store must not be queried when embedding fails")
	}
}

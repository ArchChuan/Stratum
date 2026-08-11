package vectorstore

import (
	"context"
	"errors"
	"testing"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"github.com/stretchr/testify/require"
)

type stubStore struct {
	createErr error
	insertErr error
	inserted  []storagemilvus.DocumentChunk
	searchRes []storagemilvus.SearchResult
	searchErr error
	flushErr  error
	delErr    error
	count     int64
	countErr  error

	collectionInfo storagemilvus.CollectionInfo
	// lastExpression records the filter expression of the most recent
	// SearchWithFilterStrict call for whitelist-propagation assertions.
	lastExpression string
}

func (s *stubStore) CreateCollectionWithDim(context.Context, string, int) error { return s.createErr }
func (s *stubStore) Insert(_ context.Context, _ string, docs []storagemilvus.DocumentChunk, _ string) error {
	s.inserted = docs
	return s.insertErr
}
func (s *stubStore) DescribeCollection(context.Context, string) (storagemilvus.CollectionInfo, error) {
	return s.collectionInfo, nil
}

func (s *stubStore) SearchWithFilterStrict(_ context.Context, _ string, _ []float32, _ int, expression string, _ ...string) ([]storagemilvus.SearchResult, error) {
	s.lastExpression = expression
	return s.searchRes, s.searchErr
}
func (s *stubStore) Flush(context.Context, string) error            { return s.flushErr }
func (s *stubStore) DeleteCollection(context.Context, string) error { return s.delErr }
func (s *stubStore) CountVectors(context.Context, string, string) (int64, error) {
	return s.count, s.countErr
}

var _ storeIface = (*stubStore)(nil)

func TestAdapter_CreateCollectionWithDim(t *testing.T) {
	s := &stubStore{createErr: errors.New("create boom")}
	a := &Adapter{store: s}
	require.ErrorContains(t, a.CreateCollectionWithDim(context.Background(), "col", 768), "create boom")
}

func TestAdapter_Insert_convertsDocuments(t *testing.T) {
	s := &stubStore{}
	a := &Adapter{store: s}
	docs := []knowledgeport.VectorDocument{
		{ID: "d1", Content: "c", SourceDocument: "s", ChunkIndex: 0, Vector: []float32{1, 2}},
	}
	require.NoError(t, a.Insert(context.Background(), "col", docs))
	require.Len(t, s.inserted, 1)
	require.Equal(t, "d1", s.inserted[0].ID)
	require.Equal(t, "s", s.inserted[0].SourceDocument)
	require.Equal(t, []float32{1, 2}, s.inserted[0].Vector)
}

func TestAdapter_Insert_empty(t *testing.T) {
	a := &Adapter{store: &stubStore{}}
	require.NoError(t, a.Insert(context.Background(), "col", nil))
}

func TestAdapter_Insert_storeFails(t *testing.T) {
	s := &stubStore{insertErr: errors.New("milvus down")}
	a := &Adapter{store: s}
	err := a.Insert(context.Background(), "col", []knowledgeport.VectorDocument{{ID: "d1"}})
	require.ErrorContains(t, err, "milvus down")
}

func TestAdapter_Search_convertsResults(t *testing.T) {
	s := &stubStore{searchRes: []storagemilvus.SearchResult{
		{ID: "r1", Content: "hit", SourceDocument: "doc", ChunkIndex: 3, Score: 0.91},
	}}
	a := &Adapter{store: s}
	res, err := a.Search(context.Background(), "col", []float32{0.5}, 5)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, "r1", res[0].ID)
	require.Equal(t, "doc", res[0].SourceDocument)
	require.Equal(t, int64(3), res[0].ChunkIndex)
	require.InDelta(t, 0.91, res[0].Score, 0.001)
}

func TestAdapter_Search_storeFails(t *testing.T) {
	s := &stubStore{searchErr: errors.New("search down")}
	a := &Adapter{store: s}
	res, err := a.Search(context.Background(), "col", []float32{0.5}, 5)
	require.Nil(t, res)
	require.ErrorContains(t, err, "search down")
}

func TestAdapter_Search_emptyResults(t *testing.T) {
	a := &Adapter{store: &stubStore{}}
	res, err := a.Search(context.Background(), "col", []float32{0.5}, 5)
	require.NoError(t, err)
	require.Empty(t, res)
}

func TestAdapter_SearchWithFilter_passesExpression(t *testing.T) {
	s := &stubStore{searchRes: []storagemilvus.SearchResult{
		{ID: "r1", Content: "hit", SourceDocument: "doc", ChunkIndex: 3, Score: 0.91},
	}}
	a := &Adapter{store: s}
	res, err := a.SearchWithFilter(context.Background(), "col", []float32{0.5}, 5, `source_document in ["doc"]`)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, "doc", res[0].SourceDocument)
	require.Equal(t, `source_document in ["doc"]`, s.lastExpression)
}

func TestAdapter_SearchWithFilter_emptyExpression(t *testing.T) {
	a := &Adapter{store: &stubStore{}}
	res, err := a.SearchWithFilter(context.Background(), "col", []float32{0.5}, 5, "")
	require.NoError(t, err)
	require.Empty(t, res)
}

func TestAdapter_SearchWithFilter_storeFails(t *testing.T) {
	s := &stubStore{searchErr: errors.New("search down")}
	a := &Adapter{store: s}
	res, err := a.SearchWithFilter(context.Background(), "col", []float32{0.5}, 5, "expr")
	require.Nil(t, res)
	require.ErrorContains(t, err, "search down")
}

func TestAdapter_Flush(t *testing.T) {
	s := &stubStore{flushErr: errors.New("flush boom")}
	a := &Adapter{store: s}
	require.ErrorContains(t, a.Flush(context.Background(), "col"), "flush boom")
}

func TestAdapter_DeleteCollection(t *testing.T) {
	s := &stubStore{delErr: errors.New("del boom")}
	a := &Adapter{store: s}
	require.ErrorContains(t, a.DeleteCollection(context.Background(), "col"), "del boom")
}

func TestAdapter_CountVectors(t *testing.T) {
	s := &stubStore{count: 42}
	a := &Adapter{store: s}
	n, err := a.CountVectors(context.Background(), "col")
	require.NoError(t, err)
	require.Equal(t, int64(42), n)
}

func TestAdapter_CountVectors_storeFails(t *testing.T) {
	s := &stubStore{countErr: errors.New("count down")}
	a := &Adapter{store: s}
	_, err := a.CountVectors(context.Background(), "col")
	require.ErrorContains(t, err, "count down")
}

func TestAdapter_CountVectors_collectionNotFoundIsZero(t *testing.T) {
	// A workspace that never ingested has no collection; stats must read 0,
	// not fail.
	s := &stubStore{countErr: storagemilvus.ErrCollectionNotFound}
	a := &Adapter{store: s}
	n, err := a.CountVectors(context.Background(), "col")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestAdapter_Search_collectionNotFoundPropagates(t *testing.T) {
	// RAG retrieval fails closed on a missing collection: the application
	// layer distinguishes "legitimately empty workspace" from drift.
	s := &stubStore{searchErr: storagemilvus.ErrCollectionNotFound}
	a := &Adapter{store: s}
	res, err := a.Search(context.Background(), "col", []float32{0.5}, 5)
	require.Nil(t, res)
	require.ErrorIs(t, err, storagemilvus.ErrCollectionNotFound)
}

// Package knowledge provides knowledge base and RAG services.
package knowledge

import (
	"context"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
)

type MockGraphRAG struct {
	queryResult interface{}
	queryErr    error
	connected   bool
}

func NewMockGraphRAG() *MockGraphRAG {
	return &MockGraphRAG{
		connected: true,
	}
}

func (m *MockGraphRAG) Connect(ctx context.Context) error {
	return nil
}

func (m *MockGraphRAG) CreateNode(ctx context.Context, label string, props map[string]interface{}) error {
	return nil
}

func (m *MockGraphRAG) CreateRelationship(ctx context.Context, fromID, toID, relType string) error {
	return nil
}

func (m *MockGraphRAG) Query(ctx context.Context, query string) (interface{}, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.queryResult, nil
}

func (m *MockGraphRAG) GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

func (m *MockGraphRAG) FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

func (m *MockGraphRAG) Close() error {
	return nil
}

func (m *MockGraphRAG) SetQueryResult(result interface{}) {
	m.queryResult = result
}

func (m *MockGraphRAG) SetQueryError(err error) {
	m.queryErr = err
}

type MockVectorStore struct {
	searchResults []interface{}
	searchErr     error
}

func NewMockVectorStore() *MockVectorStore {
	return &MockVectorStore{
		searchResults: []interface{}{},
	}
}

func (m *MockVectorStore) Connect(ctx context.Context) error {
	return nil
}

func (m *MockVectorStore) CreateCollection(ctx context.Context, name string) error {
	return nil
}

func (m *MockVectorStore) Insert(ctx context.Context, collection string, chunks []vector.DocumentChunk) error {
	return nil
}

func (m *MockVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int) ([]interface{}, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}

func (m *MockVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	return nil
}

func (m *MockVectorStore) Flush(ctx context.Context, collection string) error {
	return nil
}

func (m *MockVectorStore) Close() error {
	return nil
}

func (m *MockVectorStore) SetSearchResults(results []interface{}) {
	m.searchResults = results
}

func (m *MockVectorStore) SetSearchError(err error) {
	m.searchErr = err
}

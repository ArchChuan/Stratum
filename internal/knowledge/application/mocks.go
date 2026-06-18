// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/vector"
)

// Compile-time check: MockGraphStore implements port.GraphStore.
var _ knowledgeport.GraphStore = (*MockGraphStore)(nil)

type MockGraphStore struct {
	queryResult interface{}
	queryErr    error
}

func NewMockGraphStore() *MockGraphStore {
	return &MockGraphStore{}
}

func (m *MockGraphStore) Connect(_ context.Context) error {
	return nil
}

func (m *MockGraphStore) CreateNode(_ context.Context, _ string, _ map[string]interface{}) error {
	return nil
}

func (m *MockGraphStore) CreateRelationship(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *MockGraphStore) Query(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.queryResult, nil
}

func (m *MockGraphStore) GetNeighborNodes(_ context.Context, _ string, _ int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

func (m *MockGraphStore) FullTextSearch(_ context.Context, _ string, _ int) ([]knowledgeport.GraphNodeResult, error) {
	return []knowledgeport.GraphNodeResult{}, nil
}

func (m *MockGraphStore) QueryWorkspaceDocumentIDs(_ context.Context, _ string) ([]string, error) {
	return []string{}, nil
}

func (m *MockGraphStore) DeleteWorkspaceNodes(_ context.Context, _ string) error {
	return nil
}

func (m *MockGraphStore) GetWorkspaceDocCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (m *MockGraphStore) GetWorkspaceNames(_ context.Context) ([]string, error) {
	return []string{}, nil
}

func (m *MockGraphStore) Close() error {
	return nil
}

func (m *MockGraphStore) SetQueryResult(result interface{}) {
	m.queryResult = result
}

func (m *MockGraphStore) SetQueryError(err error) {
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

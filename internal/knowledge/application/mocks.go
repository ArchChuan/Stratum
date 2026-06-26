// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/pkg/vector"
)

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

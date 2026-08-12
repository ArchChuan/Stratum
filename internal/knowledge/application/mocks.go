// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

type MockVectorStore struct {
	searchResults  []knowledgeport.VectorSearchResult
	searchErr      error
	collectionInfo knowledgeport.CollectionInfo
	collectionErr  error
	// lastExpression records the filter expression of the most recent
	// SearchWithFilter call for whitelist-filter assertions.
	lastExpression string
}

func NewMockVectorStore() *MockVectorStore {
	return &MockVectorStore{
		searchResults: []knowledgeport.VectorSearchResult{},
	}
}

func (m *MockVectorStore) Connect(ctx context.Context) error {
	return nil
}

func (m *MockVectorStore) CreateCollection(ctx context.Context, name string) error {
	return nil
}

func (m *MockVectorStore) CreateCollectionWithDim(ctx context.Context, name string, dimension int) error {
	return nil
}

func (m *MockVectorStore) Insert(ctx context.Context, collection string, chunks []knowledgeport.VectorDocument) error {
	return nil
}

func (m *MockVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int) ([]knowledgeport.VectorSearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}

// SearchWithFilter mirrors Search but records the filter expression so tests
// can assert the doc whitelist reached the vector leg.
func (m *MockVectorStore) SearchWithFilter(ctx context.Context, collection string, vector []float32, topK int, expression string) ([]knowledgeport.VectorSearchResult, error) {
	m.lastExpression = expression
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}

func (m *MockVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	return nil
}

func (m *MockVectorStore) DescribeCollection(ctx context.Context, collection string) (knowledgeport.CollectionInfo, error) {
	if m.collectionErr != nil {
		return knowledgeport.CollectionInfo{}, m.collectionErr
	}
	return m.collectionInfo, nil
}

func (m *MockVectorStore) SetCollectionInfo(info knowledgeport.CollectionInfo) {
	m.collectionInfo = info
}
func (m *MockVectorStore) SetCollectionErr(err error) { m.collectionErr = err }

func (m *MockVectorStore) Flush(ctx context.Context, collection string) error {
	return nil
}

func (m *MockVectorStore) DeleteCollection(ctx context.Context, collection string) error {
	return nil
}

func (m *MockVectorStore) CountVectors(ctx context.Context, collection string) (int64, error) {
	return 0, nil
}

func (m *MockVectorStore) Close() error {
	return nil
}

func (m *MockVectorStore) SetSearchResults(results []knowledgeport.VectorSearchResult) {
	m.searchResults = results
}

func (m *MockVectorStore) SetSearchError(err error) {
	m.searchErr = err
}

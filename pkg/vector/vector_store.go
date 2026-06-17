// Package vector re-exports pkg/storage/milvus for backwards compatibility.
// New code should import pkg/storage/milvus directly. Will be removed in phase 5.
package vector

import (
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"go.uber.org/zap"
)

// VectorStore is an alias for the relocated milvus.VectorStore.
type VectorStore = storagemilvus.VectorStore

// DocumentChunk is an alias for milvus.DocumentChunk.
type DocumentChunk = storagemilvus.DocumentChunk

// SearchResult is an alias for milvus.SearchResult.
type SearchResult = storagemilvus.SearchResult

// NewVectorStore constructs a Milvus-backed vector store.
//
// New code should import github.com/byteBuilderX/stratum/pkg/storage/milvus
// directly; this re-export is retained for the Phase 1 transition period.
func NewVectorStore(host, port string, logger *zap.Logger) *VectorStore {
	return storagemilvus.NewVectorStore(host, port, logger)
}

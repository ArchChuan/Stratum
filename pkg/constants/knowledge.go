package constants

import (
	"fmt"
	"regexp"
)

const (
	// MaxUploadFileSize is the maximum file size for document uploads (100MB).
	MaxUploadFileSize = 100 * 1024 * 1024

	// CollectionPrefix is the unified prefix for all knowledge workspace collections.
	CollectionPrefix = "kb"

	// MaxChunksPerDocument caps chunk count per ingest job to bound memory
	// and processing time. Documents above this threshold are rejected up-front.
	MaxChunksPerDocument = 5000

	// MaxConcurrentIngest caps the number of concurrently running ingest jobs
	// across the process to protect embed backends and DB connection pool.
	MaxConcurrentIngest = 3

	// IngestQueueCapacity caps how many ingest jobs may be queued waiting for
	// a concurrency slot before the API returns 429.
	IngestQueueCapacity = 20

	// IngestStatusProcessing/Completed/Failed are enum values for
	// knowledge_docs.ingest_status.
	IngestStatusProcessing = "processing"
	IngestStatusCompleted  = "completed"
	IngestStatusFailed     = "failed"
)

var milvusUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// CollectionName generates the Milvus collection name for a knowledge workspace.
// workspaceID must be the stable workspace ID, not the mutable name.
// CollectionName returns the Milvus collection name for a workspace.
// workspaceID (UUID v7) is globally unique, so tenantID is ignored.
func CollectionName(_, workspaceID string) string {
	san := func(s string) string { return milvusUnsafe.ReplaceAllString(s, "_") }
	return fmt.Sprintf("%s_%s", CollectionPrefix, san(workspaceID))
}

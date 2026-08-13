package port

import "context"

// ModelCapability enumerates catalogue capabilities the knowledge boundary
// consults. Mirrors llmgateway domain capabilities locally so knowledge does
// not import llmgateway (cross-context interfaces live at the consumer).
type ModelCapability string

const (
	CapEmbedding ModelCapability = "embedding"
	CapRerank    ModelCapability = "rerank"
)

// ModelExists reports whether model exists in the global enabled catalogue
// with the given capability. Directory/database failures must propagate
// (fail closed) — consumers must not default to allow on a lookup error.
type ModelExists interface {
	Exists(ctx context.Context, model string, capability ModelCapability) (bool, error)
}

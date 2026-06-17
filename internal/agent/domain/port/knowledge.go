package port

import "context"

type KnowledgeRetriever interface {
	Retrieve(ctx context.Context, kbID, query string, topK int) ([]string, error)
}

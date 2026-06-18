// Package port defines outbound interfaces for the knowledge bounded context.
package port

import "context"

// GraphStore abstracts graph database operations for the knowledge context.
// All Cypher/query strings are encapsulated in infrastructure implementations.
type GraphStore interface {
	Connect(ctx context.Context) error
	CreateNode(ctx context.Context, label string, properties map[string]interface{}) error
	CreateRelationship(ctx context.Context, fromID, toID, relType string) error
	Query(ctx context.Context, query string, params map[string]interface{}) (interface{}, error)
	GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error)
	FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]map[string]interface{}, error)
	QueryWorkspaceDocumentIDs(ctx context.Context, workspace string) ([]string, error)
	DeleteWorkspaceNodes(ctx context.Context, workspace string) error
	Close() error
}

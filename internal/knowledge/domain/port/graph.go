// Package port defines outbound interfaces for the knowledge bounded context.
package port

import "context"

// GraphNodeResult is a domain-typed node returned by graph search operations.
// It decouples the application layer from driver-specific types (e.g. neo4j/dbtype).
type GraphNodeResult struct {
	ID         string
	Labels     []string
	Properties map[string]any
}

// GraphStore abstracts graph database operations for the knowledge context.
// All Cypher/query strings are encapsulated in infrastructure implementations.
type GraphStore interface {
	Connect(ctx context.Context) error
	CreateNode(ctx context.Context, label string, properties map[string]interface{}) error
	CreateRelationship(ctx context.Context, fromID, toID, relType string) error
	GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error)
	FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]GraphNodeResult, error)
	QueryWorkspaceDocumentIDs(ctx context.Context, workspace string) ([]string, error)
	DeleteWorkspaceNodes(ctx context.Context, workspace string) error
	GetWorkspaceDocCount(ctx context.Context, workspace string) (int, error)
	GetWorkspaceNames(ctx context.Context) ([]string, error)
	Close() error
}

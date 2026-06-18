// Package neo4j implements the knowledge GraphStore port using the Neo4j driver.
package neo4j

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"go.uber.org/zap"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// validCypherIdentifier matches safe Neo4j label and relationship type names.
var validCypherIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxQueryLimit = 1000

var luceneSpecial = strings.NewReplacer(
	`+`, `\+`, `-`, `\-`, `!`, `\!`,
	`(`, `\(`, `)`, `\)`, `{`, `\{`, `}`, `\}`,
	`[`, `\[`, `]`, `\]`, `^`, `\^`, `"`, `\"`,
	`~`, `\~`, `?`, `\?`, `:`, `\:`, `/`, `\/`,
	`\`, `\\`,
)

func validateCypherIdentifier(s string) error {
	if !validCypherIdentifier.MatchString(s) {
		return fmt.Errorf("invalid Cypher identifier: %q", s)
	}
	return nil
}

func escapeLucene(s string) string {
	return luceneSpecial.Replace(s)
}

// GraphAdapter implements knowledgeport.GraphStore using the Neo4j driver.
type GraphAdapter struct {
	mu      sync.RWMutex
	driver  neo4j.DriverWithContext
	uri     string
	user    string
	passwd  string
	logger  *zap.Logger
	session neo4j.SessionWithContext
}

// Compile-time interface satisfaction check.
var _ knowledgeport.GraphStore = (*GraphAdapter)(nil)

// NewGraphAdapter constructs a GraphAdapter and returns it as a GraphStore.
func NewGraphAdapter(uri, user, password string, logger *zap.Logger) knowledgeport.GraphStore {
	return &GraphAdapter{
		uri:    uri,
		user:   user,
		passwd: password,
		logger: logger,
	}
}

func (g *GraphAdapter) doConnect(ctx context.Context) error {
	g.logger.Info("connecting to Neo4j", zap.String("uri", g.uri))

	driver, err := neo4j.NewDriverWithContext(g.uri, neo4j.BasicAuth(g.user, g.passwd, ""))
	if err != nil {
		g.logger.Error("failed to create Neo4j driver", zap.Error(err))
		return fmt.Errorf("failed to create Neo4j driver: %w", err)
	}
	g.driver = driver

	session := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	g.session = session

	_, err = session.Run(ctx, "RETURN 1", nil)
	if err != nil {
		g.logger.Error("failed to verify Neo4j connection", zap.Error(err))
		return fmt.Errorf("failed to verify connection: %w", err)
	}

	g.logger.Info("connected to Neo4j successfully")
	return nil
}

func (g *GraphAdapter) Connect(ctx context.Context) error {
	return g.ensureConnected(ctx)
}

func (g *GraphAdapter) ensureConnected(ctx context.Context) error {
	g.mu.RLock()
	if g.session != nil {
		g.mu.RUnlock()
		return nil
	}
	g.mu.RUnlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.session != nil {
		return nil
	}
	return g.doConnect(ctx)
}

func (g *GraphAdapter) CreateNode(ctx context.Context, label string, properties map[string]interface{}) error {
	if err := g.ensureConnected(ctx); err != nil {
		return fmt.Errorf("neo4j not available: %w", err)
	}
	g.logger.Debug("creating node", zap.String("label", label))

	if err := validateCypherIdentifier(label); err != nil {
		return err
	}

	cypher := fmt.Sprintf("CREATE (n:%s $props)", label)
	result, err := g.session.Run(ctx, cypher, map[string]interface{}{
		"props": properties,
	})
	if err != nil {
		g.logger.Error("failed to create node", zap.Error(err))
		return fmt.Errorf("failed to create node: %w", err)
	}
	if result.Err() != nil {
		return result.Err()
	}
	_, err = result.Consume(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (g *GraphAdapter) CreateRelationship(ctx context.Context, fromID, toID, relType string) error {
	if err := g.ensureConnected(ctx); err != nil {
		return fmt.Errorf("neo4j not available: %w", err)
	}
	g.logger.Debug("creating relationship", zap.String("type", relType))

	if err := validateCypherIdentifier(relType); err != nil {
		return err
	}

	cypher := fmt.Sprintf(`
		MATCH (a {id: $fromId}), (b {id: $toId})
		CREATE (a)-[r:%s]->(b)
		SET r.created_at = datetime()
	`, relType)
	result, err := g.session.Run(ctx, cypher, map[string]interface{}{
		"fromId": fromID,
		"toId":   toID,
	})
	if err != nil {
		g.logger.Error("failed to create relationship", zap.Error(err))
		return fmt.Errorf("failed to create relationship: %w", err)
	}
	if result.Err() != nil {
		return result.Err()
	}
	_, err = result.Consume(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (g *GraphAdapter) Query(ctx context.Context, query string, params map[string]interface{}) (interface{}, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("neo4j not available: %w", err)
	}
	g.logger.Debug("executing graph query")

	result, err := g.session.Run(ctx, query, params)
	if err != nil {
		g.logger.Error("failed to execute query", zap.Error(err))
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	if result.Err() != nil {
		return nil, result.Err()
	}

	var results []interface{}
	records, err := result.Collect(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		results = append(results, record.Values)
	}
	return results, nil
}

func (g *GraphAdapter) GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("neo4j not available: %w", err)
	}
	if maxDepth <= 0 || maxDepth > 10 {
		return nil, fmt.Errorf("maxDepth must be between 1 and 10, got %d", maxDepth)
	}

	cypher := fmt.Sprintf(`
		MATCH (n {id: $nodeId})-[r*1..%d]->(neighbor)
		RETURN neighbor, r, n
		ORDER BY neighbor.created_at DESC
		LIMIT 20
	`, maxDepth)
	result, err := g.session.Run(ctx, cypher, map[string]interface{}{
		"nodeId": nodeID,
	})
	if err != nil {
		g.logger.Error("failed to get neighbor nodes", zap.Error(err))
		return nil, fmt.Errorf("failed to get neighbor nodes: %w", err)
	}
	if result.Err() != nil {
		return nil, result.Err()
	}

	var nodes []map[string]interface{}
	records, err := result.Collect(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		nodeMap := make(map[string]interface{})
		for _, key := range record.Keys {
			value, _ := record.Get(key)
			nodeMap[key] = value
		}
		nodes = append(nodes, nodeMap)
	}
	return nodes, nil
}

func (g *GraphAdapter) FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]knowledgeport.GraphNodeResult, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("neo4j not available: %w", err)
	}
	if strings.TrimSpace(searchTerm) == "" {
		return nil, fmt.Errorf("searchTerm must not be empty")
	}
	if limit <= 0 || limit > maxQueryLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d, got %d", maxQueryLimit, limit)
	}

	cypher := `
		CALL db.index.fulltext.queryNodes('entity_fulltext', $searchTerm) YIELD node, score
		RETURN node, score
		ORDER BY score DESC
		LIMIT $limit
	`
	result, err := g.session.Run(ctx, cypher, map[string]interface{}{
		"searchTerm": escapeLucene(searchTerm) + "*",
		"limit":      limit,
	})
	if err != nil {
		g.logger.Error("failed to perform full text search", zap.Error(err))
		return nil, fmt.Errorf("failed to perform full text search: %w", err)
	}
	if result.Err() != nil {
		return nil, result.Err()
	}

	records, err := result.Collect(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]knowledgeport.GraphNodeResult, 0, len(records))
	for _, record := range records {
		raw, ok := record.Get("node")
		if !ok {
			continue
		}
		n, ok := raw.(dbtype.Node)
		if !ok {
			g.logger.Warn("unexpected node type in FullTextSearch", zap.String("type", fmt.Sprintf("%T", raw)))
			continue
		}
		// Use the application-managed "id" property (string UUID), not n.Id (internal rowid).
		domainID, _ := n.Props["id"].(string)
		nodes = append(nodes, knowledgeport.GraphNodeResult{
			ID:         domainID,
			Labels:     n.Labels,
			Properties: n.Props,
		})
	}
	return nodes, nil
}

// QueryWorkspaceDocumentIDs returns the IDs of all Document nodes for the given
// workspace without modifying any data. Used to collect IDs before Milvus deletion
// so that a retry can re-query if the downstream step fails.
func (g *GraphAdapter) QueryWorkspaceDocumentIDs(ctx context.Context, workspace string) ([]string, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("neo4j not available: %w", err)
	}
	queryResult, err := g.session.Run(ctx,
		`MATCH (d:Document {workspace: $workspace}) RETURN d.id AS id`,
		map[string]interface{}{"workspace": workspace},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query workspace documents: %w", err)
	}
	records, err := queryResult.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to collect workspace document IDs: %w", err)
	}
	docIDs := make([]string, 0, len(records))
	for _, rec := range records {
		if id, ok := rec.Get("id"); ok {
			if s, ok := id.(string); ok {
				docIDs = append(docIDs, s)
			}
		}
	}
	return docIDs, nil
}

// DeleteWorkspaceNodes deletes all Document and DocumentChunk nodes for the given
// workspace. Call QueryWorkspaceDocumentIDs first to obtain IDs for Milvus cleanup
// before invoking this, so that a retry remains safe: if this step already ran,
// QueryWorkspaceDocumentIDs returns an empty slice and Milvus deletion is a no-op.
func (g *GraphAdapter) DeleteWorkspaceNodes(ctx context.Context, workspace string) error {
	if err := g.ensureConnected(ctx); err != nil {
		return fmt.Errorf("neo4j not available: %w", err)
	}
	_, err := g.session.Run(ctx,
		`MATCH (d:Document {workspace: $workspace})
		 OPTIONAL MATCH (d)-[:HAS_CHUNK]->(c:DocumentChunk)
		 DETACH DELETE d, c`,
		map[string]interface{}{"workspace": workspace},
	)
	if err != nil {
		return fmt.Errorf("failed to delete workspace nodes: %w", err)
	}
	g.logger.Info("deleted workspace graph nodes", zap.String("workspace", workspace))
	return nil
}

// GetWorkspaceDocCount returns the number of Document nodes for the given workspace.
func (g *GraphAdapter) GetWorkspaceDocCount(ctx context.Context, workspace string) (int, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return 0, fmt.Errorf("neo4j not available: %w", err)
	}
	result, err := g.session.Run(ctx,
		`MATCH (d:Document) WHERE d.workspace = $workspace RETURN count(d) AS doc_count`,
		map[string]interface{}{"workspace": workspace},
	)
	if err != nil {
		return 0, fmt.Errorf("graph: workspace doc count: %w", err)
	}
	records, err := result.Collect(ctx)
	if err != nil {
		return 0, fmt.Errorf("graph: collect doc count: %w", err)
	}
	if len(records) == 0 {
		return 0, nil
	}
	raw, _ := records[0].Get("doc_count")
	if c, ok := raw.(int64); ok {
		return int(c), nil
	}
	return 0, nil
}

// GetWorkspaceNames returns all distinct workspace names present in the graph.
func (g *GraphAdapter) GetWorkspaceNames(ctx context.Context) ([]string, error) {
	if err := g.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("neo4j not available: %w", err)
	}
	result, err := g.session.Run(ctx,
		`MATCH (d:Document) WITH d.workspace AS workspace RETURN DISTINCT workspace ORDER BY workspace`,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("graph: workspace names: %w", err)
	}
	records, err := result.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph: collect workspace names: %w", err)
	}
	names := make([]string, 0, len(records))
	for _, rec := range records {
		if raw, ok := rec.Get("workspace"); ok {
			if s, ok := raw.(string); ok {
				names = append(names, s)
			}
		}
	}
	return names, nil
}

func (g *GraphAdapter) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.logger.Info("closing Neo4j connection")
	if g.session != nil {
		g.session.Close(context.Background()) //nolint:errcheck,gosec
		g.session = nil
	}
	if g.driver != nil {
		err := g.driver.Close(context.Background())
		g.driver = nil
		return err
	}
	return nil
}

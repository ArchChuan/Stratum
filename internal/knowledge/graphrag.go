// Package knowledge provides knowledge base and RAG services.
package knowledge

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"go.uber.org/zap"
)

// validCypherIdentifier matches safe Neo4j label and relationship type names.
var validCypherIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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

type GraphRAG struct {
	driver  neo4j.DriverWithContext
	uri     string
	user    string
	passwd  string
	logger  *zap.Logger
	session neo4j.SessionWithContext
}

func NewGraphRAG(uri, user, password string, logger *zap.Logger) *GraphRAG {
	return &GraphRAG{
		uri:    uri,
		user:   user,
		passwd: password,
		logger: logger,
	}
}

func (g *GraphRAG) Connect(ctx context.Context) error {
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

func (g *GraphRAG) CreateNode(ctx context.Context, label string, properties map[string]interface{}) error {
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

func (g *GraphRAG) CreateRelationship(ctx context.Context, fromID, toID, relType string) error {
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

func (g *GraphRAG) Query(ctx context.Context, query string) (interface{}, error) {
	g.logger.Debug("executing graph query")

	result, err := g.session.Run(ctx, query, nil)
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

func (g *GraphRAG) GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error) {
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

func (g *GraphRAG) FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]map[string]interface{}, error) {
	if strings.TrimSpace(searchTerm) == "" {
		return nil, fmt.Errorf("searchTerm must not be empty")
	}
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("limit must be between 1 and 1000, got %d", limit)
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

	var results []map[string]interface{}
	records, err := result.Collect(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		resultMap := make(map[string]interface{})
		for _, key := range record.Keys {
			value, _ := record.Get(key)
			resultMap[key] = value
		}
		results = append(results, resultMap)
	}
	return results, nil
}

func (g *GraphRAG) Close() error {
	g.logger.Info("closing Neo4j connection")
	if g.session != nil {
		g.session.Close(context.Background()) //nolint:errcheck,gosec
	}
	if g.driver != nil {
		return g.driver.Close(context.Background())
	}
	return nil
}

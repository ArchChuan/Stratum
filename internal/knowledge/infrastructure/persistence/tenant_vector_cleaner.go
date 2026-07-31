package persistence

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"go.uber.org/zap"
)

// TenantVectorCleaner drops all Milvus collections for a tenant:
//   - memory collection: "memory_<tenantID with dashes replaced by underscores>"
//   - memory facts collection: "memory_facts_<tenantID with dashes replaced by underscores>"
//   - knowledge collection: "tenant_<tenantID with dashes replaced by underscores>_kb"
//   - RAG workspace collections: "<workspace_name>_kb" (queried from PG)
type TenantVectorCleaner struct {
	pool   *pgxpool.Pool
	vs     *milvus.VectorStore
	logger *zap.Logger
}

// NewTenantVectorCleaner wires the dependencies.
func NewTenantVectorCleaner(pool *pgxpool.Pool, vs *milvus.VectorStore, logger *zap.Logger) *TenantVectorCleaner {
	return &TenantVectorCleaner{pool: pool, vs: vs, logger: logger}
}

// DropTenantCollections deletes all Milvus collections belonging to tenantID.
// Each drop is attempted independently; errors are joined and returned.
func (c *TenantVectorCleaner) DropTenantCollections(ctx context.Context, tenantID string) error {
	if c.vs == nil {
		return nil
	}

	tid := strings.ReplaceAll(tenantID, "-", "_")
	var errs []string

	for _, col := range []string{
		"memory_" + tid,
		"memory_facts_" + tid,
		"tenant_" + tid + "_kb", // KnowledgeCollection — single-collection-per-tenant model
	} {
		if err := c.vs.DeleteCollection(ctx, col); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", col, err))
		}
	}

	// knowledge workspace collections — keyed by workspace UUID
	if c.pool != nil {
		schema := fmt.Sprintf("tenant_%s", tenantID)
		rows, err := c.pool.Query(ctx,
			fmt.Sprintf(`SELECT id FROM "%s".rag_workspaces`, schema),
		)
		if err != nil {
			c.logger.Warn("failed to query rag_workspaces for vector cleanup, workspace collections may leak",
				zap.String("tenant_id", tenantID), zap.Error(err))
		} else {
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					continue
				}
				col := constants.CollectionName(tenantID, id)
				if err := c.vs.DeleteCollection(ctx, col); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", col, err))
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("drop tenant collections: %s", strings.Join(errs, "; "))
	}
	return nil
}

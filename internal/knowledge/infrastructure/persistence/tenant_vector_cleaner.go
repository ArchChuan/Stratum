package persistence

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

// TenantVectorCleaner drops all Milvus collections for a tenant:
//   - memory collection: "memory_<tenantID with dashes replaced by underscores>"
//   - RAG workspace collections: "<workspace_name>_kb" (queried from PG)
type TenantVectorCleaner struct {
	pool *pgxpool.Pool
	vs   *milvus.VectorStore
}

// NewTenantVectorCleaner wires the dependencies.
func NewTenantVectorCleaner(pool *pgxpool.Pool, vs *milvus.VectorStore) *TenantVectorCleaner {
	return &TenantVectorCleaner{pool: pool, vs: vs}
}

// DropTenantCollections deletes all Milvus collections belonging to tenantID.
// Each drop is attempted independently; errors are joined and returned.
func (c *TenantVectorCleaner) DropTenantCollections(ctx context.Context, tenantID string) error {
	if c.vs == nil {
		return nil
	}

	var errs []string

	// memory collection
	memCol := "memory_" + strings.ReplaceAll(tenantID, "-", "_")
	if err := c.vs.DeleteCollection(ctx, memCol); err != nil {
		errs = append(errs, fmt.Sprintf("memory collection %s: %v", memCol, err))
	}

	// RAG workspace collections — query workspace names from PG
	if c.pool != nil {
		schema := fmt.Sprintf("tenant_%s", tenantID)
		rows, err := c.pool.Query(ctx,
			fmt.Sprintf(`SELECT name FROM "%s".rag_workspaces`, schema),
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					continue
				}
				col := name + "_kb"
				if err := c.vs.DeleteCollection(ctx, col); err != nil {
					errs = append(errs, fmt.Sprintf("rag collection %s: %v", col, err))
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("drop tenant collections: %s", strings.Join(errs, "; "))
	}
	return nil
}

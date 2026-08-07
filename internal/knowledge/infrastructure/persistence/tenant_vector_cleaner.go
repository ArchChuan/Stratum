package persistence

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"go.uber.org/zap"
)

// tenantIDRE mirrors the UUID format enforced by the IAM tenant cleaner
// (internal/iam/infrastructure/persistence/tenant_schema_cleaner.go): tenant
// schemas are only ever provisioned for UUID tenant IDs, so anything else is
// rejected before it can reach SQL or Milvus collection naming.
var tenantIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// collectionDropper narrows the vector store to what cleanup needs so tests
// can inject a stub without a live Milvus.
type collectionDropper interface {
	DeleteCollection(ctx context.Context, collectionName string) error
}

var _ collectionDropper = (*milvus.VectorStore)(nil)

// TenantVectorCleaner drops all Milvus collections for a tenant:
//   - memory collection: "memory_<tenantID with dashes replaced by underscores>"
//   - memory facts collection: "memory_facts_<tenantID with dashes replaced by underscores>"
//   - knowledge collection: "tenant_<tenantID with dashes replaced by underscores>_kb"
//   - RAG workspace collections: "<workspace_name>_kb" (queried from PG)
type TenantVectorCleaner struct {
	pool   poolIface
	vs     collectionDropper
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
	if !tenantIDRE.MatchString(tenantID) {
		return fmt.Errorf("tenant_vector_cleaner: invalid tenantID format")
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

	c.dropWorkspaceCollections(ctx, tenantID, &errs)

	if len(errs) > 0 {
		return fmt.Errorf("drop tenant collections: %s", strings.Join(errs, "; "))
	}
	return nil
}

// dropWorkspaceCollections queries the tenant's rag_workspaces and drops the
// matching Milvus collections. The tenant schema is reached through execTenant
// so the lookup stays inside the tenant boundary instead of interpolating the
// schema name into SQL.
//
// 两阶段：事务内只做快速 SELECT（short-lived read-only），commit 后再在
// 事务外逐个删集合——Milvus 删除不可随 DB 事务回滚，放在事务内会造成
// "事务失败但向量已删"的漂移，且 DB 事务时长受 Milvus 可用性影响。
// Drop failures are appended to errs; a failed lookup is only logged
// (best-effort cleanup) so the fixed-name collections still get dropped.
func (c *TenantVectorCleaner) dropWorkspaceCollections(ctx context.Context, tenantID string, errs *[]string) {
	if c.pool == nil {
		return
	}
	var cols []string
	queryErr := execTenant(ctx, c.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM rag_workspaces`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				continue
			}
			cols = append(cols, constants.CollectionName(tenantID, id))
		}
		return rows.Err()
	})
	if queryErr != nil {
		c.logger.Warn("failed to query rag_workspaces for vector cleanup, workspace collections may leak",
			zap.String("tenant_id", tenantID), zap.Error(queryErr))
		return
	}
	for _, col := range cols {
		if err := c.vs.DeleteCollection(ctx, col); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: %v", col, err))
		}
	}
}

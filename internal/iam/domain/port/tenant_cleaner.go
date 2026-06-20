package port

import "context"

// TenantSchemaCleaner drops the tenant's PostgreSQL schema and all its data.
type TenantSchemaCleaner interface {
	DropTenantSchema(ctx context.Context, tenantID string) error
}

// TenantVectorCleaner drops all Milvus collections belonging to the tenant
// (memory collection + all RAG workspace collections).
type TenantVectorCleaner interface {
	DropTenantCollections(ctx context.Context, tenantID string) error
}

// TenantCacheInvalidator evicts all in-process cache entries for the tenant.
type TenantCacheInvalidator interface {
	Invalidate(tenantID string)
}

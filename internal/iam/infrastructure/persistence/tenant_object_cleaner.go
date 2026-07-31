package persistence

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
)

// TenantObjectCleaner deletes all MinIO objects belonging to a tenant.
type TenantObjectCleaner struct {
	store  pkgobjectstore.Store
	bucket string
	logger *zap.Logger
}

// NewTenantObjectCleaner creates a TenantObjectCleaner.
// store may be nil (e.g. MinIO not configured); in that case DropTenantObjects is a no-op.
func NewTenantObjectCleaner(store pkgobjectstore.Store, bucket string, logger *zap.Logger) *TenantObjectCleaner {
	return &TenantObjectCleaner{store: store, bucket: bucket, logger: logger}
}

// DropTenantObjects removes all objects whose key starts with "<tenantID>/" from the bucket.
func (c *TenantObjectCleaner) DropTenantObjects(ctx context.Context, tenantID string) error {
	if c.store == nil || c.bucket == "" {
		return nil
	}
	prefix := tenantID + "/"
	if err := c.store.DeleteByPrefix(ctx, c.bucket, prefix); err != nil {
		return fmt.Errorf("drop tenant objects: %w", err)
	}
	c.logger.Info("deleted tenant objects", zap.String("tenant_id", tenantID))
	return nil
}

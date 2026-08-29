package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/infrastructure/hermes"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	knowledgepersistence "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// tenantModelCacheInvalidator adapts the global ModelRegistry to IAM's
// tenant-keyed cache invalidator. providers/models 已提升为 public 全局目录，
// 租户删除不改动全局解析状态；仍全量刷新以防陈旧 provider 配置残留。
type tenantModelCacheInvalidator struct {
	registry *llmgateway.ModelRegistry
}

func (t tenantModelCacheInvalidator) Invalidate(_ string) {
	if t.registry != nil {
		t.registry.Invalidate()
	}
}

// BuildHermesFuncs returns start/stop/healthCheck closures for the NATS
// hermes component. It reuses the platform-wide NATS connection from
// Storage（由 pkg/messaging/nats.Connect 创建，MaxReconnects(-1)），
// 保证重连/超时配置与其余组件一致，且不重复建连。
func BuildHermesFuncs(c *Container, logger *zap.Logger) (
	start func(context.Context) error,
	stop func(context.Context) error,
	healthCheck func(context.Context) error,
) {
	var client *hermes.Client
	start = func(_ context.Context) error {
		if c.Storage == nil || c.Storage.NATS == nil {
			logger.Warn("NATS not available, hermes disabled")
			return nil
		}
		hermesClient, err := hermes.NewClient(c.Storage.NATS, logger, observability.NoopMetrics{})
		if err != nil {
			logger.Warn("Failed to create hermes client", zap.Error(err))
			return nil
		}
		client = hermesClient
		logger.Info("hermes client started")
		return nil
	}
	stop = func(_ context.Context) error {
		if client != nil {
			client.Close()
		}
		return nil
	}
	healthCheck = func(_ context.Context) error {
		if c.Storage == nil || c.Storage.NATS == nil {
			return fmt.Errorf("NATS not available")
		}
		return nil
	}
	return
}

// IAM holds identity & access management bounded-context services.
type IAM struct {
	TenantService     *application.TenantService
	InvitationService *application.InvitationService
	AdminService      *application.AdminService
	// TenantRepo exposes public tenant-registry queries to other wiring
	// consumers (e.g. knowledge startup iterates active tenants).
	TenantRepo *iampersistence.TenantRepo
}

func (c *Container) buildIAM(_ context.Context) error {
	iam := &IAM{}
	db := c.dbOrNil()
	if db != nil && c.Platform != nil {
		repo := iampersistence.NewTenantRepo(db)
		iam.TenantRepo = repo
		iam.TenantService = application.NewTenantService(
			repo,
			c.Logger,
		)
		iam.InvitationService = application.NewInvitationService(iampersistence.NewInvitationRepo(db))
		opts := []application.AdminServiceOption{
			application.WithSchemaCleaner(iampersistence.NewTenantSchemaCleaner(db)),
			application.WithAdminLogger(c.Logger),
			application.WithUserRepo(iampersistence.NewAdminUserRepo(db)),
			application.WithCacheInvalidator(tenantModelCacheInvalidator{c.Platform.ModelRegistry}),
		}
		if c.Storage != nil && c.Storage.Milvus != nil {
			opts = append(opts, application.WithVectorCleaner(
				knowledgepersistence.NewTenantVectorCleaner(db, c.Storage.Milvus, c.Logger),
			))
		}
		if c.RevisionObjectStore != nil {
			opts = append(opts, application.WithObjectCleaner(
				iampersistence.NewTenantObjectCleaner(c.RevisionObjectStore, c.Config.TracePayload.Bucket, c.Logger),
			))
		}
		iam.AdminService = application.NewAdminService(
			iampersistence.NewAdminTenantRepo(db),
			opts...,
		)
	}
	c.IAM = iam
	return nil
}

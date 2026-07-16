package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/infrastructure/hermes"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	knowledgepersistence "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
)

// BuildHermesFuncs returns start/stop/healthCheck closures for the NATS
// hermes component. Constructing the client here keeps platform/runtime
// free of iam/infrastructure imports.
func BuildHermesFuncs(cfg *config.Config, logger *zap.Logger) (
	start func(context.Context) error,
	stop func(context.Context) error,
	healthCheck func(context.Context) error,
) {
	var client *hermes.Client
	start = func(_ context.Context) error {
		c, err := hermes.NewClient(cfg.NatsURL, logger)
		if err != nil {
			logger.Warn("Failed to connect to NATS", zap.Error(err))
			return nil
		}
		client = c
		logger.Info("Connected to NATS", zap.String("url", cfg.NatsURL))
		return nil
	}
	stop = func(_ context.Context) error {
		if client != nil {
			client.Close()
		}
		return nil
	}
	healthCheck = func(_ context.Context) error {
		if cfg.NatsURL == "" {
			return fmt.Errorf("NATS URL not configured")
		}
		return nil
	}
	return
}

// IAM holds identity & access management bounded-context services.
type IAM struct {
	TenantService *application.TenantService
	AdminService  *application.AdminService
}

func (c *Container) buildIAM(_ context.Context) error {
	iam := &IAM{}
	db := c.dbOrNil()
	if db != nil && c.Platform != nil {
		repo := iampersistence.NewTenantRepo(db)
		iam.TenantService = application.NewTenantService(
			repo,
			c.Logger,
			c.Platform.AESKey,
			c.Platform.GatewayCache,
		)
		opts := []application.AdminServiceOption{
			application.WithSchemaCleaner(iampersistence.NewTenantSchemaCleaner(db)),
			application.WithAdminLogger(c.Logger),
		}
		if c.Storage != nil && c.Storage.Milvus != nil {
			opts = append(opts, application.WithVectorCleaner(
				knowledgepersistence.NewTenantVectorCleaner(db, c.Storage.Milvus),
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

package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	knowledgepersistence "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
)

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

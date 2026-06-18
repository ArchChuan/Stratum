package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
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
			c.Config.FrontendURL,
			c.Platform.AESKey,
			c.Platform.GatewayCache,
		)
		iam.AdminService = application.NewAdminService(iampersistence.NewAdminTenantRepo(db))
	}
	c.IAM = iam
	return nil
}

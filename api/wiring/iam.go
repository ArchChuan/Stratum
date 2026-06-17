package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
)

// IAM holds identity & access management bounded-context services.
type IAM struct {
	TenantService *application.TenantService
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
	}
	c.IAM = iam
	return nil
}

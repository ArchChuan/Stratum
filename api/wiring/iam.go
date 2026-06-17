package wiring

import "context"

// IAM is reserved for the identity & access management bounded context.
// Currently authentication primitives (JWT, GitHub, token store, onboarding)
// live on Container.Platform; this struct will hold IAM-specific services
// (e.g. invitation service, role manager) once they are extracted.
type IAM struct{}

func (c *Container) buildIAM(_ context.Context) error {
	c.IAM = &IAM{}
	return nil
}

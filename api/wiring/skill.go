package wiring

import (
	"context"

	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skillpersist "github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence"
)

// Skill owns versioned instruction bundles. It intentionally exposes no
// executable gateway: Agent is the only runtime that can activate a Skill.
type Skill struct {
	VersionService *skillapp.VersionService
}

func (c *Container) buildSkill(_ context.Context) error {
	db := c.dbOrNil()
	if db == nil {
		c.Skill = &Skill{}
		return nil
	}
	svc := skillapp.NewVersionService(skillpersist.NewPgSkillRevisionRepo(db), c.Logger)
	svc.SetEditorRepo(skillpersist.NewPgSkillResourceEditorRepo(db))
	svc.SetActorNameResolver(iampersistence.NewPgActorNameResolver(db))
	c.Skill = &Skill{VersionService: svc}
	return nil
}

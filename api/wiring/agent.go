package wiring

import (
	"context"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
)

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time.
type Agent struct {
	Registry       *agent.Registry
	ExecStore      agent.ExecutionStore
	ChatStore      agent.ChatStore
	TenantResolver agentport.TenantCapabilityResolver
	SkillLookup    agentport.SkillLookup
	TenantSettings agentport.TenantSettings
}

func (c *Container) buildAgent(_ context.Context) error {
	db := c.dbOrNil()

	var registry *agent.Registry
	if db != nil {
		registry = agent.NewRegistry(persistence.NewPgAgentRepo(db), c.Logger)
	} else {
		registry = agent.NewRegistry(nil, c.Logger)
	}
	if c.Skill != nil && c.Skill.CapGateway != nil {
		registry.SetCapGateway(c.Skill.CapGateway)
	}
	if c.Memory != nil && c.Memory.Injector != nil {
		registry.SetMemoryInjector(c.Memory.Injector)
	}
	if c.Memory != nil && c.Memory.RecallFn != nil {
		registry.SetRecallMemoryFn(c.Memory.RecallFn)
	}

	a := &Agent{Registry: registry}
	if db != nil {
		a.ExecStore = persistence.NewPgExecutionStore(db)
		a.ChatStore = persistence.NewPgChatStore(db)
		a.SkillLookup = persistence.NewPgSkillLookup(db)
		a.TenantSettings = persistence.NewPgTenantSettings(db)
		if c.Skill != nil {
			a.TenantResolver = newTenantCapabilityResolver(db, c.Platform.AESKey, c.Platform.GatewayCache, c.Skill.SkillAdapter, c.Logger)
		}
	}
	c.Agent = a
	return nil
}

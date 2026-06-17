package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent"
)

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time.
type Agent struct {
	Registry  *agent.Registry
	ExecStore *agent.ExecutionStore
	ChatStore agent.ChatStore
}

func (c *Container) buildAgent(_ context.Context) error {
	db := c.dbOrNil()
	registry := agent.NewRegistry(db, c.Logger)
	if c.Skill != nil && c.Skill.CapGateway != nil {
		registry.SetCapGateway(c.Skill.CapGateway)
	}
	if c.Memory != nil && c.Memory.Injector != nil {
		registry.SetMemoryInjector(c.Memory.Injector)
	}

	a := &Agent{Registry: registry}
	if db != nil {
		a.ExecStore = agent.NewExecutionStore(db)
		a.ChatStore = agent.NewPgChatStore(db)
	}
	c.Agent = a
	return nil
}

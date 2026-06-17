package wiring

import (
	"context"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/capgateway"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	skillgateway "github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway/providers"
)

// Skill groups the skill execution stack: the sandboxed code executor,
// the SkillGateway with its registered providers, and the capgateway
// adapters that bridge LLM/Skill capabilities under the unified
// CapabilityGateway facade consumed by agents.
type Skill struct {
	CodeExecutor *code.CodeExecutor
	Gateway      *skillgateway.DefaultGateway
	LLMAdapter   *capgateway.LLMAdapter
	SkillAdapter *capgateway.SkillAdapter
	CapGateway   capgateway.CapabilityGateway
}

func (c *Container) buildSkill(_ context.Context) error {
	codeExec := code.NewCodeExecutor(code.DefaultCodeExecutorConfig())
	gw := skillgateway.NewDefaultGateway(c.Platform.Metrics, c.Logger, nil)

	db := c.dbOrNil()
	if db != nil {
		if err := gw.RegisterProvider(providers.NewDBSkillAdapter(db, c.LLMGateway.Gateway, c.Logger, codeExec)); err != nil {
			c.Logger.Warn("failed to register DB skill provider", zap.Error(err))
		}
	}

	llmAdapter := capgateway.NewLLMAdapter(c.LLMGateway.Gateway, c.Logger)
	skillAdapter := capgateway.NewSkillAdapter(gw, c.Logger)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, skillAdapter, c.Logger)

	c.Skill = &Skill{
		CodeExecutor: codeExec,
		Gateway:      gw,
		LLMAdapter:   llmAdapter,
		SkillAdapter: skillAdapter,
		CapGateway:   capGW,
	}
	return nil
}

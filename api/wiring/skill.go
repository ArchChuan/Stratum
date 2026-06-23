package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	skilldomainport "github.com/byteBuilderX/stratum/internal/skill/domain/port"
	skillinfra "github.com/byteBuilderX/stratum/internal/skill/infrastructure"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	skillgateway "github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway/providers"
	skillpersist "github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence"
)

// Skill groups the skill execution stack.
type Skill struct {
	CodeExecutor *code.CodeExecutor
	Gateway      *skillgateway.DefaultGateway
	SkillAdapter *capgateway.SkillAdapter
	Service      *skillapp.SkillService
}

// wiringSkillFactory implements skilldomainport.SkillFactory in the composition root.
type wiringSkillFactory struct {
	executor *code.CodeExecutor
	analyzer skilldomainport.CodeAnalyzer
	logger   *zap.Logger
}

func newWiringSkillFactory(executor *code.CodeExecutor, analyzer skilldomainport.CodeAnalyzer, logger *zap.Logger) skilldomainport.SkillFactory {
	return &wiringSkillFactory{executor: executor, analyzer: analyzer, logger: logger}
}

func (f *wiringSkillFactory) Build(id string, in skilldomainport.SkillInput) (skilldomain.Skill, error) {
	switch in.Type {
	case "code":
		if r := f.analyzer.Check(in.Language, in.Code); !r.Safe {
			return nil, &skilldomain.AnalysisError{Reasons: r.Reasons}
		}
		return code.NewCodeSkillWithExecutor(id, in.Name, in.Description, in.Code, in.Language, f.executor), nil
	case "llm":
		// completer is nil; LLMSkill.Execute resolves it from ctx at call time.
		return executors.NewLLMSkill(id, in.Name, in.Description, in.SystemPrompt, in.Model, in.Temperature, in.MaxTokens, nil, f.logger), nil
	case "http":
		return executors.NewHTTPSkill(id, in.Name, in.Description, in.URL, in.Method, in.Headers, in.BodyTemplate, in.TimeoutSec)
	default:
		return nil, fmt.Errorf("%w: %s", skilldomain.ErrSkillUnsupportedType, in.Type)
	}
}

func (c *Container) buildSkill(_ context.Context) error {
	codeExec := code.NewCodeExecutor(code.DefaultCodeExecutorConfig())
	analyzer := skillinfra.NewStaticAnalyzer()
	factory := newWiringSkillFactory(codeExec, analyzer, c.Logger)

	gw := skillgateway.NewDefaultGateway(c.Platform.Metrics, c.Logger, nil)

	db := c.dbOrNil()
	if db != nil {
		// completer is nil; DBSkillAdapter resolves it from ctx via InjectCompleter.
		if err := gw.RegisterProvider(providers.NewDBSkillAdapter(db, c.Logger, codeExec)); err != nil {
			c.Logger.Warn("failed to register DB skill provider", zap.Error(err))
		}
	}

	skillAdapter := capgateway.NewSkillAdapter(gw, c.Logger)

	var svc *skillapp.SkillService
	if db != nil {
		repo := skillpersist.NewPgSkillRepo(db)
		svc = skillapp.NewSkillService(repo, factory, c.Logger)
	}

	c.Skill = &Skill{
		CodeExecutor: codeExec,
		Gateway:      gw,
		SkillAdapter: skillAdapter,
		Service:      svc,
	}
	return nil
}

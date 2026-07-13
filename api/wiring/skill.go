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
	CodeExecutor   *code.CodeExecutor
	Gateway        *skillgateway.DefaultGateway
	SkillAdapter   *capgateway.SkillAdapter
	Service        *skillapp.SkillService
	VersionService *skillapp.VersionService
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
	case "prompt":
		return executors.NewPromptSkill(id, in.Name, in.Description, in.PromptTemplate), nil
	default:
		return nil, fmt.Errorf("%w: %s", skilldomain.ErrSkillUnsupportedType, in.Type)
	}
}

// skillGatewayAdapter bridges *skillgateway.DefaultGateway to the primitive
// interface that capgateway.SkillAdapter requires, keeping
// agent/infrastructure/capability free of skill/infrastructure imports.
type skillGatewayAdapter struct {
	gw *skillgateway.DefaultGateway
}

func (a *skillGatewayAdapter) Execute(ctx context.Context, traceID, skillID, versionID string, input any) (any, error) {
	resp, err := a.gw.Execute(ctx, skillgateway.SkillRequest{
		TraceID:   traceID,
		SkillID:   skillID,
		VersionID: versionID,
		Input:     input,
	})
	if err != nil {
		return nil, err
	}
	return resp.Output, nil
}

type skillRunnerAdapter struct {
	gw *skillgateway.DefaultGateway
}

func (a *skillRunnerAdapter) RunSkill(ctx context.Context, skillID string, input any, traceID string) (skillapp.SkillTestResult, error) {
	resp, err := a.gw.Execute(ctx, skillgateway.SkillRequest{
		TraceID: traceID,
		SkillID: skillID,
		Input:   input,
		Metadata: map[string]string{
			"caller": "skill_test",
		},
	})
	if err != nil {
		return skillapp.SkillTestResult{}, err
	}
	return skillapp.SkillTestResult{
		TraceID:  resp.TraceID,
		SkillID:  resp.SkillID,
		Output:   resp.Output,
		Duration: resp.Duration,
	}, nil
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

	skillAdapter := capgateway.NewSkillAdapter(&skillGatewayAdapter{gw: gw}, c.Logger)

	var svc *skillapp.SkillService
	var versionSvc *skillapp.VersionService
	if db != nil {
		repo := skillpersist.NewPgSkillRepo(db)
		svc = skillapp.NewSkillService(repo, factory, c.Logger, &skillRunnerAdapter{gw: gw})
		versionRepo := skillpersist.NewPgSkillVersionRepo(db)
		versionSvc = skillapp.NewVersionService(versionRepo, c.Logger)
	}

	c.Skill = &Skill{
		CodeExecutor:   codeExec,
		Gateway:        gw,
		SkillAdapter:   skillAdapter,
		Service:        svc,
		VersionService: versionSvc,
	}
	return nil
}

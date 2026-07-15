package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalpersist "github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/skilladapter"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Evaluation struct {
	Service             *evalapp.Service
	SuiteService        *evalapp.SuiteService
	JobService          *evalapp.JobService
	Worker              *evalapp.Worker
	OptimizationService *evalapp.OptimizationService
	ExperimentService   *evalapp.ExperimentService
	FeedbackService     *evalapp.FeedbackService
}

type skillCandidateManager struct {
	versions *skillapp.VersionService
}

type experimentSkillRevisionResolver struct {
	service *evalapp.ExperimentService
}

func (r experimentSkillRevisionResolver) ResolveSkillRevision(
	ctx context.Context,
	tenantID, skillID, subjectID string,
) (string, bool, error) {
	return r.service.ResolveRevision(ctx, tenantID, evaldomain.ResourceKindSkill, skillID, subjectID)
}

func (m skillCandidateManager) LoadOptimizableSnapshot(
	ctx context.Context, _ string, baseline evaldomain.ResourceRef,
) (map[string]any, error) {
	version, err := m.versions.GetVersion(ctx, baseline.ResourceID, baseline.RevisionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mode":    version.Implementation.Mode,
		"source":  version.Implementation.Source,
		"runtime": version.Implementation.Runtime,
	}, nil
}

func (m skillCandidateManager) CreateCandidate(
	ctx context.Context, _ string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	version, err := m.versions.CreateCandidate(ctx, baseline.ResourceID, baseline.RevisionID, skillapp.CandidateInput{
		Source: patch.Source, ParameterPatch: patch.ParameterPatch, PromptPatch: patch.PromptPatch,
		GenerationMetadata: map[string]any{"rationale": patch.Rationale},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	return evaldomain.ResourceRef{
		Kind: baseline.Kind, ResourceID: baseline.ResourceID, RevisionID: version.ID,
	}, nil
}

type gatewayPromptRewriter struct {
	resolver agentport.TenantCapabilityResolver
}

func (r gatewayPromptRewriter) Rewrite(
	ctx context.Context, request evalapp.PromptRewriteRequest,
) ([]evaldomain.CandidatePatch, error) {
	gateway, keys, ok := r.resolver.Resolve(ctx, request.TenantID)
	if !ok || gateway == nil {
		return nil, fmt.Errorf("prompt optimizer: tenant has no LLM provider configured")
	}
	snapshotJSON, err := json.Marshal(request.BaselineSnapshot)
	if err != nil {
		return nil, err
	}
	failuresJSON, err := json.Marshal(request.FailureSummaries)
	if err != nil {
		return nil, err
	}
	response, err := gateway.Route(ctx, agentport.CapabilityRequest{
		TenantID:   request.TenantID,
		Type:       agentport.CapLLM,
		LLMAPIKeys: keys,
		Timeout:    60 * time.Second,
		LLM: &agentport.LLMCapRequest{
			Model: "qwen-plus", Temperature: 0.2, MaxTokens: 2048,
			Messages: []agentport.LLMMessage{
				{Role: "system", Content: "你是提示词优化器。只生成候选内容，不决定发布。仅输出 JSON 数组。"},
				{Role: "user", Content: fmt.Sprintf(
					"基线配置：%s\n失败摘要：%s\n输出最多3项，每项格式：{\"prompt_patch\":{\"promptTemplate\":\"...\"},\"rationale\":\"...\"}。不得修改权限、密钥或网络配置。",
					string(snapshotJSON), string(failuresJSON),
				)},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return parsePromptRewritePatches(response.Content)
}

func parsePromptRewritePatches(content string) ([]evaldomain.CandidatePatch, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var patches []evaldomain.CandidatePatch
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &patches); err != nil {
		return nil, fmt.Errorf("prompt optimizer: parse candidate patches: %w", err)
	}
	if len(patches) == 0 || len(patches) > 3 {
		return nil, fmt.Errorf("prompt optimizer: expected 1-3 candidate patches")
	}
	for i := range patches {
		if err := evaldomain.ValidatePromptPatch(patches[i].PromptPatch); err != nil {
			return nil, err
		}
		patches[i].Source = "llm_rewrite"
	}
	return patches, nil
}

type evaluationTenantLister struct {
	pool *pgxpool.Pool
}

func (l evaluationTenantLister) ListTenantIDs(ctx context.Context) ([]string, error) {
	schemas, err := postgres.ListTenantSchemas(ctx, l.pool)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		ids = append(ids, strings.TrimPrefix(schema, "tenant_"))
	}
	return ids, nil
}

func (c *Container) buildEvaluation(ctx context.Context) error {
	db := c.dbOrNil()
	if db == nil || c.Skill == nil || c.Skill.VersionExecutor == nil {
		c.Evaluation = &Evaluation{}
		return nil
	}
	suiteRepo := evalpersist.NewPgSuiteRepository(db)
	runRepo := evalpersist.NewPgRunRepository(db)
	jobRepo := evalpersist.NewPgJobRepository(db)
	optimizationRepo := evalpersist.NewPgOptimizationRepository(db)
	experimentRepo := evalpersist.NewPgExperimentRepository(db)
	feedbackRepo := evalpersist.NewPgFeedbackRepository(db)
	adapter := skilladapter.New(c.Skill.VersionExecutor)
	service := evalapp.NewService(adapter, runRepo, suiteRepo)
	suiteService := evalapp.NewSuiteService(suiteRepo)
	jobService := evalapp.NewJobService(jobRepo, service)
	manager := skillCandidateManager{versions: c.Skill.VersionService}
	var rewriter evalapp.PromptRewriter
	if c.Agent != nil && c.Agent.TenantResolver != nil {
		rewriter = gatewayPromptRewriter{resolver: c.Agent.TenantResolver}
	}
	optimizationService := evalapp.NewOptimizationService(manager, rewriter, optimizationRepo)
	experimentService := evalapp.NewExperimentService(experimentRepo)
	feedbackService := evalapp.NewFeedbackService(feedbackRepo, experimentService)
	worker := evalapp.NewWorker(evaluationTenantLister{pool: db}, jobService, time.Second)
	worker.Start(ctx)
	c.shutdown = append(c.shutdown, func(context.Context) error {
		worker.Stop()
		return nil
	})
	c.Evaluation = &Evaluation{
		Service: service, SuiteService: suiteService, JobService: jobService, Worker: worker,
		OptimizationService: optimizationService,
		ExperimentService:   experimentService,
		FeedbackService:     feedbackService,
	}
	if c.Agent != nil && c.Agent.Service != nil {
		c.Agent.Service.SetSkillRevisionResolver(experimentSkillRevisionResolver{service: experimentService})
	}
	return nil
}

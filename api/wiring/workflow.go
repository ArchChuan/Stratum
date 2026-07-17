package wiring

import (
	"context"
	"fmt"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowexec "github.com/byteBuilderX/stratum/internal/workflow/infrastructure/executor"
	workflowpersist "github.com/byteBuilderX/stratum/internal/workflow/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
)

type workflowAgentService interface {
	Execute(context.Context, string, agentapp.ExecRequest, agentapp.ExecMeta) (*agentapp.AgentResult, int, error)
	ExecuteSkillScenario(context.Context, string, agentapp.ExecRequest, agentapp.ExecMeta, agentport.SkillActivation) (*agentapp.AgentResult, int, error)
}

type workflowAgentExecutor struct{ agents workflowAgentService }

func (e workflowAgentExecutor) ExecuteAgent(ctx context.Context, tenantID, agentID, input string) (string, string, error) {
	traceID := uuid.Must(uuid.NewV7()).String()
	result, _, err := e.agents.Execute(ctx, agentID, agentapp.ExecRequest{Query: input, UserID: "workflow"}, agentapp.ExecMeta{TenantID: tenantID, TraceID: traceID})
	if err != nil {
		return "", traceID, err
	}
	return result.Output, traceID, nil
}

type workflowSkillVersions interface {
	ResolveActivation(context.Context, string, string) (skillapp.SkillActivationView, bool, error)
}

type workflowSkillExecutor struct {
	agents   workflowAgentService
	versions workflowSkillVersions
}

func (e workflowSkillExecutor) ExecuteSkill(ctx context.Context, tenantID, agentID, skillID, revisionID, input string) (string, string, error) {
	view, found, err := e.versions.ResolveActivation(ctx, skillID, revisionID)
	if err != nil {
		return "", "", err
	}
	if !found || view.RevisionID != revisionID {
		return "", "", fmt.Errorf("pinned skill revision not found")
	}
	activation := agentport.SkillActivation{SkillID: view.SkillID, RevisionID: view.RevisionID, Name: view.Name, Description: view.Description, Instructions: view.Instructions, MCPToolIDs: view.MCPToolIDs, KnowledgeWorkspaceIDs: view.KnowledgeWorkspaceIDs, MemoryScopes: view.MemoryScopes}
	traceID := uuid.Must(uuid.NewV7()).String()
	result, _, err := e.agents.ExecuteSkillScenario(ctx, agentID, agentapp.ExecRequest{Query: input, UserID: "workflow"}, agentapp.ExecMeta{TenantID: tenantID, TraceID: traceID}, activation)
	if err != nil {
		return "", traceID, err
	}
	return result.Output, traceID, nil
}

type workflowMCPPolicy interface {
	GetToolRisk(context.Context, string, string) (mcpdomain.ToolRiskLevel, error)
}
type workflowMCPManager interface {
	CallTool(context.Context, string, string, interface{}) (interface{}, error)
}

type workflowMCPExecutor struct {
	policies workflowMCPPolicy
	manager  workflowMCPManager
}

func workflowMCPTenantContext(ctx context.Context, tenantID string) (context.Context, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("workflow MCP tenant is required")
	}
	return postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "workflow-worker", Role: postgres.RoleTenantAdmin}), nil
}

func (e workflowMCPExecutor) ToolRisk(ctx context.Context, tenantID string, serverID, toolName string) (workflowexec.ToolRisk, error) {
	ctx, err := workflowMCPTenantContext(ctx, tenantID)
	if err != nil {
		return "", err
	}
	risk, err := e.policies.GetToolRisk(ctx, serverID, toolName)
	return workflowexec.ToolRisk(risk), err
}
func (e workflowMCPExecutor) CallTool(ctx context.Context, tenantID string, serverID, toolName string, input map[string]any) (any, error) {
	ctx, err := workflowMCPTenantContext(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return e.manager.CallTool(ctx, serverID, toolName, input)
}

type Workflow struct {
	DefinitionService *workflowapp.DefinitionService
	RunService        *workflowapp.RunService
	ControlService    *workflowapp.ControlService
	Worker            interface {
		Run(context.Context, time.Duration)
	}
}

type workflowRunAdvancer struct{ runs *workflowapp.RunService }

func (a workflowRunAdvancer) Execute(ctx context.Context, tenantID, runID string) error {
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "workflow-worker", Role: postgres.RoleTenantAdmin})
	return a.runs.Execute(tenantCtx, tenantID, runID)
}

func (c *Container) buildWorkflow(_ context.Context) error {
	db := c.dbOrNil()
	if db == nil || c.Agent == nil || c.Agent.Service == nil {
		return nil
	}
	store := workflowpersist.NewPgStore(db)
	newID := func() string { return uuid.Must(uuid.NewV7()).String() }
	agentExecutor := workflowAgentExecutor{agents: c.Agent.Service}
	registry := workflowexec.NewRegistry(agentExecutor, workflowSkillExecutor{agents: c.Agent.Service, versions: c.Skill.VersionService}, workflowMCPExecutor{policies: c.MCP.Service, manager: c.MCP.Manager})
	runs := workflowapp.NewRunServiceWithRegistry(store, store, registry, newID)
	c.Workflow = &Workflow{DefinitionService: workflowapp.NewDefinitionService(store, store, newID), RunService: runs, ControlService: workflowapp.NewControlService(store, newID)}
	c.Workflow.Worker = workflowapp.NewWorker("workflow-"+newID(), store, workflowRunAdvancer{runs: runs}, 30*time.Second)
	return nil
}

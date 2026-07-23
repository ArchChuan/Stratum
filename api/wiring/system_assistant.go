package wiring

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"golang.org/x/sync/errgroup"
)

const diagnosticCollectorConcurrency = 3

type diagnosticAreaCollector func(
	context.Context, domain.DiagnosticRequest,
) ([]domain.DiagnosticFact, []domain.EvidenceGap, error)

type systemAssistantDiagnosticAdapter struct {
	mu         sync.RWMutex
	roles      agentport.TenantRoleResolver
	collectors map[domain.DiagnosticArea]diagnosticAreaCollector
}

func (a *systemAssistantDiagnosticAdapter) setSkillEvaluationReader(
	service skillDiagnosticService, evaluations skillEvaluationReader, memberBindings memberResourceBindingProvider,
) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.collectors[domain.DiagnosticAreaSkill] = skillDiagnosticCollector(service, evaluations, memberBindings)
}

func newSystemAssistantDiagnosticAdapter(
	roles agentport.TenantRoleResolver, collectors map[domain.DiagnosticArea]diagnosticAreaCollector,
) *systemAssistantDiagnosticAdapter {
	return &systemAssistantDiagnosticAdapter{roles: roles, collectors: collectors}
}

func (a *systemAssistantDiagnosticAdapter) Collect(
	ctx context.Context, request domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	roleCtx, roleCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	req, err := agentapp.AuthorizeDiagnosticRequest(roleCtx, a.roles, request)
	roleCancel()
	if err != nil {
		return domain.DiagnosticEvidence{}, err
	}
	evidence := domain.DiagnosticEvidence{Scope: req.Scope, CollectedAt: time.Now().UTC()}
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(diagnosticCollectorConcurrency)
	for _, area := range req.Areas {
		area := area
		group.Go(func() error {
			a.mu.RLock()
			collector := a.collectors[area]
			a.mu.RUnlock()
			if collector == nil {
				mu.Lock()
				evidence.Gaps = append(evidence.Gaps, domain.EvidenceGap{Area: area, Code: domain.DiagnosticGapUnavailable})
				mu.Unlock()
				return nil
			}
			readCtx, cancel := context.WithTimeout(groupCtx, constants.AgentDBQueryTimeout)
			facts, gaps, collectErr := collector(readCtx, req)
			cancel()
			mu.Lock()
			defer mu.Unlock()
			if collectErr != nil {
				evidence.Gaps = append(evidence.Gaps, domain.EvidenceGap{Area: area, Code: diagnosticSafeGapCode(collectErr)})
				return nil
			}
			evidence.Facts = append(evidence.Facts, filterDiagnosticFacts(req, facts)...)
			evidence.Gaps = append(evidence.Gaps, gaps...)
			return nil
		})
	}
	_ = group.Wait()
	sort.SliceStable(evidence.Facts, func(i, j int) bool {
		if evidence.Facts[i].Area == evidence.Facts[j].Area {
			return evidence.Facts[i].ObjectID < evidence.Facts[j].ObjectID
		}
		return evidence.Facts[i].Area < evidence.Facts[j].Area
	})
	sort.SliceStable(evidence.Gaps, func(i, j int) bool { return evidence.Gaps[i].Area < evidence.Gaps[j].Area })
	return evidence, nil
}

func diagnosticSafeGapCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return domain.DiagnosticGapTimeout
	case errors.Is(err, context.Canceled):
		return domain.DiagnosticGapCancelled
	default:
		return domain.DiagnosticGapUnavailable
	}
}

func filterDiagnosticFacts(req domain.DiagnosticRequest, facts []domain.DiagnosticFact) []domain.DiagnosticFact {
	out := make([]domain.DiagnosticFact, 0, len(facts))
	for _, fact := range facts {
		if req.Scope == domain.DiagnosticScopeSelf && fact.Area == domain.DiagnosticAreaAgent &&
			fact.SubjectUserID != req.UserID {
			continue
		}
		fact.SubjectUserID = ""
		out = append(out, fact)
	}
	return out
}

type tenantRoleAdapter struct{ service tenantMemberRoleService }

func (a tenantRoleAdapter) ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error) {
	if a.service == nil {
		return "", domain.ErrDiagnosticForbidden
	}
	return a.service.GetMemberRole(ctx, tenantID, userID)
}

func systemAssistantDiagnosticCollectors(c *Container, a *Agent) map[domain.DiagnosticArea]diagnosticAreaCollector {
	collectors := map[domain.DiagnosticArea]diagnosticAreaCollector{}
	var memberBindings memberResourceBindingProvider
	if a != nil && a.EvidenceProvider != nil && a.Registry != nil {
		memberBindings = traceAgentBindingResolver{evidence: a.EvidenceProvider, registry: a.Registry}
	}
	if a != nil && a.EvidenceProvider != nil {
		collectors[domain.DiagnosticAreaAgent] = agentDiagnosticCollector(a.EvidenceProvider)
	}
	if c.Skill != nil && c.Skill.VersionService != nil {
		collectors[domain.DiagnosticAreaSkill] = skillDiagnosticCollector(c.Skill.VersionService, nil, memberBindings)
	}
	if c.MCP != nil && c.MCP.Service != nil {
		collectors[domain.DiagnosticAreaMCP] = mcpDiagnosticCollector(c.MCP.Service, memberBindings)
	}
	if c.Knowledge != nil && c.Knowledge.WorkspaceService != nil {
		collectors[domain.DiagnosticAreaKnowledge] = knowledgeDiagnosticCollector(c.Knowledge.WorkspaceService, memberBindings)
	}
	if a != nil && a.TenantResolver != nil {
		if diagnostics, ok := a.TenantResolver.(agentport.TenantModelDiagnosticProvider); ok {
			collectors[domain.DiagnosticAreaModel] = modelDiagnosticCollector(diagnostics)
		}
	}
	return collectors
}

func diagnosticTenantContext(ctx context.Context, req domain.DiagnosticRequest) context.Context {
	role := postgres.RoleTenantUser
	if req.Scope == domain.DiagnosticScopeTenant {
		role = postgres.RoleTenantAdmin
	}
	return postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: req.TenantID, UserID: req.UserID, Role: role})
}

func agentDiagnosticCollector(provider agentport.TraceEvidenceProvider) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
		opts := domain.ListOptions{Page: 1, PageSize: 20}
		if req.Scope == domain.DiagnosticScopeSelf {
			opts.UserID = req.UserID
		}
		records, _, err := provider.ListExecutions(ctx, req.TenantID, opts)
		if err != nil {
			return nil, nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(records))
		for _, record := range records {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaAgent, ObjectID: record.ID,
				Statement: "execution_status=" + record.Status, Source: "agent_trace", ObservedAt: record.CreatedAt,
				SubjectUserID: record.UserID})
		}
		return facts, nil, nil
	}
}

type skillDiagnosticService interface {
	ListSkills(context.Context) ([]skillapp.SkillProduct, error)
}

type skillEvaluationStatus struct {
	ExperimentID string
	Status       string
}

type skillEvaluationReader interface {
	ResolveSkillEvaluation(context.Context, string, string) (skillEvaluationStatus, error)
}

type memberResourceBindings struct {
	SkillIDs     map[string]struct{}
	MCPToolIDs   map[string]struct{}
	WorkspaceIDs map[string]struct{}
}

type memberResourceBindingProvider interface {
	ResolveMemberBindings(context.Context, domain.DiagnosticRequest) (memberResourceBindings, error)
}

type traceAgentBindingResolver struct {
	evidence agentport.TraceEvidenceProvider
	registry *agentapp.Registry
}

func (r traceAgentBindingResolver) ResolveMemberBindings(
	ctx context.Context, req domain.DiagnosticRequest,
) (memberResourceBindings, error) {
	bindings := memberResourceBindings{SkillIDs: map[string]struct{}{}, MCPToolIDs: map[string]struct{}{}, WorkspaceIDs: map[string]struct{}{}}
	records, _, err := r.evidence.ListExecutions(ctx, req.TenantID, domain.ListOptions{Page: 1, PageSize: 20, UserID: req.UserID})
	if err != nil {
		return bindings, err
	}
	ctx = diagnosticTenantContext(ctx, req)
	seen := map[string]struct{}{}
	for _, record := range records {
		if record.AgentID == "" {
			continue
		}
		if _, ok := seen[record.AgentID]; ok {
			continue
		}
		seen[record.AgentID] = struct{}{}
		agent, found, getErr := r.registry.Get(ctx, record.AgentID)
		if getErr != nil {
			return bindings, getErr
		}
		if !found {
			continue
		}
		cfg := agent.GetConfig()
		for _, id := range cfg.AllowedSkills {
			bindings.SkillIDs[id] = struct{}{}
		}
		for _, id := range cfg.MCPToolIDs {
			bindings.MCPToolIDs[id] = struct{}{}
		}
		for _, id := range cfg.KnowledgeWorkspaceIDs {
			bindings.WorkspaceIDs[id] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return bindings, domain.ErrDiagnosticEvidenceUnavailable
	}
	return bindings, nil
}

func skillDiagnosticCollector(service skillDiagnosticService, evaluations skillEvaluationReader, memberBindings ...memberResourceBindingProvider) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
		items, err := service.ListSkills(diagnosticTenantContext(ctx, req))
		if err != nil {
			return nil, nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(items)*4)
		gaps := make([]domain.EvidenceGap, 0)
		observedAt := time.Now().UTC()
		var allowed map[string]struct{}
		if req.Scope == domain.DiagnosticScopeSelf {
			if len(memberBindings) == 0 || memberBindings[0] == nil {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
			resolved, resolveErr := memberBindings[0].ResolveMemberBindings(ctx, req)
			if resolveErr != nil {
				return nil, nil, resolveErr
			}
			allowed = resolved.SkillIDs
			if len(allowed) == 0 {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
		}
		for _, item := range items {
			if req.Scope == domain.DiagnosticScopeSelf {
				if _, ok := allowed[item.ID]; !ok || item.ActiveRevisionID == "" || item.Status != "published" {
					continue
				}
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.ID,
					Statement: "skill_status=published", Source: "skill_public_status", ObservedAt: observedAt})
				continue
			}
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.ID,
				Statement: "skill_status=" + item.Status, Source: "skill_catalog", ObservedAt: observedAt})
			if item.ActiveRevisionID != "" {
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.ActiveRevisionID,
					Statement: "revision_status=active", Source: "skill_revision", ObservedAt: observedAt})
			}
			if item.DraftRevisionID != "" {
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.DraftRevisionID,
					Statement: "revision_status=draft", Source: "skill_revision", ObservedAt: observedAt})
			}
			if evaluations == nil {
				gaps = append(gaps, domain.EvidenceGap{Area: domain.DiagnosticAreaSkill, Code: domain.DiagnosticGapUnavailable})
				continue
			}
			status, evaluationErr := evaluations.ResolveSkillEvaluation(ctx, req.TenantID, item.ID)
			if evaluationErr != nil {
				gaps = append(gaps, domain.EvidenceGap{Area: domain.DiagnosticAreaSkill, Code: diagnosticSafeGapCode(evaluationErr)})
				continue
			}
			if status.ExperimentID != "" {
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: status.ExperimentID,
					Statement: "evaluation_status=" + status.Status, Source: "skill_evaluation", ObservedAt: observedAt})
			} else {
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.ID,
					Statement: "evaluation_status=not_configured", Source: "skill_evaluation", ObservedAt: observedAt})
			}
		}
		if req.Scope == domain.DiagnosticScopeSelf && len(facts) == 0 {
			return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
		}
		return facts, gaps, nil
	}
}

func mcpDiagnosticCollector(service *mcpapp.MCPService, memberBindings ...memberResourceBindingProvider) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
		ctx = diagnosticTenantContext(ctx, req)
		servers := service.ListServers(ctx)
		status := service.ServerStatus(ctx)
		policies, err := service.ListToolPolicies(ctx)
		if err != nil {
			return nil, nil, err
		}
		observedAt := time.Now().UTC()
		if req.Scope == domain.DiagnosticScopeSelf {
			if len(memberBindings) == 0 || memberBindings[0] == nil {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
			resolved, resolveErr := memberBindings[0].ResolveMemberBindings(ctx, req)
			if resolveErr != nil {
				return nil, nil, resolveErr
			}
			if len(resolved.MCPToolIDs) == 0 {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
			facts := make([]domain.DiagnosticFact, 0)
			for _, policy := range policies {
				toolID := "mcp:" + policy.ServerID + ":" + policy.ToolName
				if _, ok := resolved.MCPToolIDs[toolID]; !ok {
					continue
				}
				facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaMCP, ObjectID: toolID,
					Statement: "tool_policy=" + string(policy.RiskLevel), Source: "mcp_public_policy", ObservedAt: observedAt})
			}
			if len(facts) == 0 {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
			return facts, nil, nil
		}
		facts := []domain.DiagnosticFact{{Area: domain.DiagnosticAreaMCP, Statement: diagnosticMCPStatus(status), Source: "mcp_status", ObservedAt: observedAt}}
		for _, server := range servers {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaMCP, ObjectID: server.ID,
				Statement: "server_status=" + server.Status, Source: "mcp_server", ObservedAt: server.LastUpdated})
		}
		for _, policy := range policies {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaMCP,
				ObjectID: policy.ServerID + ":" + policy.ToolName, Statement: "tool_policy=" + string(policy.RiskLevel),
				Source: "mcp_tool_policy", ObservedAt: observedAt})
		}
		return facts, nil, nil
	}
}

func diagnosticMCPStatus(status mcpapp.ServerStatusBreakdown) string {
	return "servers_total=" + strconv.Itoa(status.Total) + ",connected=" + strconv.Itoa(status.Connected) +
		",disconnected=" + strconv.Itoa(status.Disconnected) + ",error=" + strconv.Itoa(status.Error)
}

func knowledgeDiagnosticCollector(service *knowledgeapp.WorkspaceService, memberBindings ...memberResourceBindingProvider) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
		ctx = diagnosticTenantContext(ctx, req)
		workspaces, err := service.ListWorkspaces(ctx, req.TenantID)
		if err != nil {
			return nil, nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(workspaces))
		var allowed map[string]struct{}
		if req.Scope == domain.DiagnosticScopeSelf {
			if len(memberBindings) == 0 || memberBindings[0] == nil {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
			resolved, resolveErr := memberBindings[0].ResolveMemberBindings(ctx, req)
			if resolveErr != nil {
				return nil, nil, resolveErr
			}
			allowed = resolved.WorkspaceIDs
			if len(allowed) == 0 {
				return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
			}
		}
		for _, workspace := range workspaces {
			if req.Scope == domain.DiagnosticScopeSelf {
				if _, ok := allowed[workspace.ID]; !ok {
					continue
				}
			}
			documents, listErr := service.ListDocuments(ctx, req.TenantID, workspace.Name)
			if listErr != nil {
				return nil, nil, listErr
			}
			objectID := workspace.ID
			if req.Scope == domain.DiagnosticScopeSelf {
				objectID = workspace.Name
			}
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaKnowledge, ObjectID: objectID,
				Statement: knowledgeIngestSummary(documents), Source: "knowledge_workspace", ObservedAt: workspace.UpdatedAt})
		}
		if req.Scope == domain.DiagnosticScopeSelf && len(facts) == 0 {
			return nil, nil, domain.ErrDiagnosticEvidenceUnavailable
		}
		return facts, nil, nil
	}
}

func knowledgeIngestSummary(documents []knowledgeapp.DocumentView) string {
	processing, completed, failed := 0, 0, 0
	for _, document := range documents {
		switch document.IngestStatus {
		case "processing":
			processing++
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}
	return "documents_total=" + strconv.Itoa(len(documents)) + ",processing=" + strconv.Itoa(processing) +
		",completed=" + strconv.Itoa(completed) + ",failed=" + strconv.Itoa(failed)
}

func modelDiagnosticCollector(provider agentport.TenantModelDiagnosticProvider) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
		status, err := provider.DiagnosticModelStatus(ctx, req.TenantID)
		if err != nil {
			return nil, nil, err
		}
		statement := "model_configured=false"
		if status.Configured {
			statement = "model_configured=true"
		}
		return []domain.DiagnosticFact{{Area: domain.DiagnosticAreaModel, Statement: statement,
			Source: "tenant_model_configuration", ObservedAt: time.Now().UTC()}}, nil, nil
	}
}

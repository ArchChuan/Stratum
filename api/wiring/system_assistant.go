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

type diagnosticAreaCollector func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, error)

type systemAssistantDiagnosticAdapter struct {
	roles      agentport.TenantRoleResolver
	collectors map[domain.DiagnosticArea]diagnosticAreaCollector
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
			collector := a.collectors[area]
			if collector == nil {
				mu.Lock()
				evidence.Gaps = append(evidence.Gaps, domain.EvidenceGap{Area: area, Code: domain.DiagnosticGapUnavailable})
				mu.Unlock()
				return nil
			}
			readCtx, cancel := context.WithTimeout(groupCtx, constants.AgentDBQueryTimeout)
			facts, collectErr := collector(readCtx, req)
			cancel()
			mu.Lock()
			defer mu.Unlock()
			if collectErr != nil {
				evidence.Gaps = append(evidence.Gaps, domain.EvidenceGap{Area: area, Code: diagnosticSafeGapCode(collectErr)})
				return nil
			}
			evidence.Facts = append(evidence.Facts, filterDiagnosticFacts(req, facts)...)
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
		if req.Scope == domain.DiagnosticScopeSelf && fact.SubjectUserID != "" && fact.SubjectUserID != req.UserID {
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
	if a != nil && a.EvidenceProvider != nil {
		collectors[domain.DiagnosticAreaAgent] = agentDiagnosticCollector(a.EvidenceProvider)
	}
	if c.Skill != nil && c.Skill.VersionService != nil {
		collectors[domain.DiagnosticAreaSkill] = skillDiagnosticCollector(c.Skill.VersionService)
	}
	if c.MCP != nil && c.MCP.Service != nil {
		collectors[domain.DiagnosticAreaMCP] = mcpDiagnosticCollector(c.MCP.Service)
	}
	if c.Knowledge != nil && c.Knowledge.WorkspaceService != nil {
		collectors[domain.DiagnosticAreaKnowledge] = knowledgeDiagnosticCollector(c.Knowledge.WorkspaceService)
	}
	if a != nil && a.TenantResolver != nil {
		collectors[domain.DiagnosticAreaModel] = modelDiagnosticCollector(a.TenantResolver)
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
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, error) {
		records, _, err := provider.ListExecutions(ctx, req.TenantID, domain.ListOptions{Page: 1, PageSize: 20})
		if err != nil {
			return nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(records))
		for _, record := range records {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaAgent, ObjectID: record.ID,
				Statement: "execution_status=" + record.Status, Source: "agent_trace", ObservedAt: record.CreatedAt,
				SubjectUserID: record.UserID})
		}
		return facts, nil
	}
}

func skillDiagnosticCollector(service *skillapp.VersionService) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, error) {
		if req.Scope == domain.DiagnosticScopeSelf {
			return nil, domain.ErrDiagnosticEvidenceUnavailable
		}
		items, err := service.ListSkills(diagnosticTenantContext(ctx, req))
		if err != nil {
			return nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(items))
		for _, item := range items {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaSkill, ObjectID: item.ID,
				Statement: "skill_status=" + item.Status, Source: "skill_catalog", ObservedAt: time.Now().UTC()})
		}
		return facts, nil
	}
}

func mcpDiagnosticCollector(service *mcpapp.MCPService) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, error) {
		if req.Scope == domain.DiagnosticScopeSelf {
			return nil, domain.ErrDiagnosticEvidenceUnavailable
		}
		ctx = diagnosticTenantContext(ctx, req)
		servers := service.ListServers(ctx)
		status := service.ServerStatus(ctx)
		policies, err := service.ListToolPolicies(ctx)
		if err != nil {
			return nil, err
		}
		observedAt := time.Now().UTC()
		facts := []domain.DiagnosticFact{{Area: domain.DiagnosticAreaMCP, Statement: diagnosticMCPStatus(status), Source: "mcp_status", ObservedAt: observedAt}}
		for _, server := range servers {
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaMCP, ObjectID: server.ID,
				Statement: "server_status=" + server.Status, Source: "mcp_server", ObservedAt: server.LastUpdated})
		}
		facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaMCP,
			Statement: "tool_policy_count=" + strconv.Itoa(len(policies)), Source: "mcp_tool_policy", ObservedAt: observedAt})
		return facts, nil
	}
}

func diagnosticMCPStatus(status mcpapp.ServerStatusBreakdown) string {
	return "servers_total=" + strconv.Itoa(status.Total) + ",connected=" + strconv.Itoa(status.Connected) +
		",disconnected=" + strconv.Itoa(status.Disconnected) + ",error=" + strconv.Itoa(status.Error)
}

func knowledgeDiagnosticCollector(service *knowledgeapp.WorkspaceService) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, error) {
		if req.Scope == domain.DiagnosticScopeSelf {
			return nil, domain.ErrDiagnosticEvidenceUnavailable
		}
		ctx = diagnosticTenantContext(ctx, req)
		workspaces, err := service.ListWorkspaces(ctx, req.TenantID)
		if err != nil {
			return nil, err
		}
		facts := make([]domain.DiagnosticFact, 0, len(workspaces))
		for _, workspace := range workspaces {
			documents, listErr := service.ListDocuments(ctx, req.TenantID, workspace.Name)
			if listErr != nil {
				return nil, listErr
			}
			facts = append(facts, domain.DiagnosticFact{Area: domain.DiagnosticAreaKnowledge, ObjectID: workspace.ID,
				Statement: knowledgeIngestSummary(documents), Source: "knowledge_workspace", ObservedAt: workspace.UpdatedAt})
		}
		return facts, nil
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

func modelDiagnosticCollector(resolver agentport.TenantCapabilityResolver) diagnosticAreaCollector {
	return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, error) {
		_, keys, ok := resolver.Resolve(ctx, req.TenantID)
		statement := "model_available=false"
		if ok && len(keys) > 0 {
			statement = "model_available=true"
		}
		if req.Scope == domain.DiagnosticScopeSelf && !ok {
			statement = "model_available=false;action=contact_admin"
		}
		return []domain.DiagnosticFact{{Area: domain.DiagnosticAreaModel, Statement: statement,
			Source: "tenant_model_configuration", ObservedAt: time.Now().UTC()}}, nil
	}
}

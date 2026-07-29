package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func TestParseAgentTypeWireIsCompatibilityOnly(t *testing.T) {
	for _, value := range []string{"react", "planning", "cot", "tool_calling", "rag", "swarm", "legacy"} {
		if got := parseAgentTypeWire(value); got != domain.ReActAgent {
			t.Fatalf("parseAgentTypeWire(%q) = %q, want react", value, got)
		}
	}
}

func TestRevisionExecutionContextUsesAgentBudget(t *testing.T) {
	ctx, cancel := revisionExecutionContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("revision execution has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > constants.AgentExecTimeout {
		t.Fatalf("revision execution budget = %v", remaining)
	}
}

type optionCaptureAgent struct {
	config    *AgentConfig
	gateway   port.CapabilityGateway
	compactor port.HistoryCompactor
}

type completionFailureCheckpoint struct {
	err error
}

func (f completionFailureCheckpoint) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}
func (f completionFailureCheckpoint) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, errors.New("unused")
}
func (f completionFailureCheckpoint) MarkCompleted(context.Context, string, string) error {
	return f.err
}
func (f completionFailureCheckpoint) DeleteExpired(context.Context, string) (int64, error) {
	return 0, nil
}

func TestCompleteApprovalResumePropagatesCheckpointPersistenceFailure(t *testing.T) {
	persistErr := errors.New("checkpoint unavailable")
	err := completeApprovalResume(
		context.Background(), completionFailureCheckpoint{err: persistErr}, "tenant-1", "execution-1", nil,
	)
	if !errors.Is(err, persistErr) {
		t.Fatalf("completeApprovalResume() error = %v, want %v", err, persistErr)
	}
}

type tenantResolverFake struct{ gateway port.CapabilityGateway }

func (f tenantResolverFake) Resolve(context.Context, string) (port.CapabilityGateway, map[string]string, bool) {
	return f.gateway, nil, true
}

func (tenantResolverFake) InjectCompleter(ctx context.Context, _ string) context.Context { return ctx }

type capabilityGatewayFake struct{}

func (capabilityGatewayFake) Route(context.Context, port.CapabilityRequest) (port.CapabilityResponse, error) {
	return port.CapabilityResponse{}, nil
}

type historyCompactorFake struct{}

func (historyCompactorFake) CompactHistory(context.Context, []port.LLMMessage) (string, error) {
	return "summary", nil
}

type knowledgeRevisionResolverFake struct {
	assignment      port.KnowledgeRevisionAssignment
	assignmentCalls int
	loadCalls       int
}

func (f *knowledgeRevisionResolverFake) ResolveKnowledgeRevision(
	context.Context, string, string, string,
) (port.KnowledgeRevisionAssignment, bool, error) {
	f.assignmentCalls++
	return f.assignment, true, nil
}

func (f *knowledgeRevisionResolverFake) LoadKnowledgeRevision(
	context.Context, string, string, string,
) (port.KnowledgeRetrievalRevision, error) {
	f.loadCalls++
	return f.assignment.Revision, nil
}

type knowledgeRevisionSearchFake struct {
	mutableCalls  int
	revisionCalls int
	revision      port.KnowledgeRetrievalRevision
	revisionErr   error
}

func (f *knowledgeRevisionSearchFake) SearchKnowledge(
	context.Context, string, []string, string, int,
) (string, error) {
	f.mutableCalls++
	return "", errors.New("mutable knowledge search must not be used")
}

func (f *knowledgeRevisionSearchFake) SearchKnowledgeRevision(
	_ context.Context, _ string, revision port.KnowledgeRetrievalRevision, _ string,
) (string, error) {
	f.revisionCalls++
	f.revision = revision
	if f.revisionErr != nil {
		return "", f.revisionErr
	}
	return "canary knowledge", nil
}

type evidenceProviderFake struct {
	tenantID string
	userID   string
}

func (f *evidenceProviderFake) ListExecutions(
	_ context.Context, tenantID string, options domain.ListOptions,
) ([]domain.ExecutionRecord, int64, error) {
	f.tenantID = tenantID
	f.userID = options.UserID
	return []domain.ExecutionRecord{{ID: "execution-1", TraceID: "trace-1", Status: domain.ExecStatusSuccess}}, 1, nil
}

func (f *evidenceProviderFake) ToolObservations(
	context.Context, string, string,
) ([]domain.ToolObservation, error) {
	return []domain.ToolObservation{{ToolName: "search"}}, nil
}

func (f *evidenceProviderFake) TraceEvents(
	context.Context, string, string,
) ([]domain.AgentTraceEvent, error) {
	return []domain.AgentTraceEvent{{SpanName: "react.llm"}}, nil
}

func (f *evidenceProviderFake) Resolve(context.Context, string, string) (domain.TraceEvidence, error) {
	return domain.TraceEvidence{UserID: "user-1"}, nil
}

func (f *evidenceProviderFake) ResolveBatch(
	context.Context, string, []string,
) (map[string]domain.TraceEvidence, error) {
	return map[string]domain.TraceEvidence{}, nil
}

func (a *optionCaptureAgent) GetConfig() *AgentConfig                      { return a.config }
func (a *optionCaptureAgent) SetCapGateway(gateway port.CapabilityGateway) { a.gateway = gateway }
func (a *optionCaptureAgent) SetHistoryCompactor(compactor port.HistoryCompactor) {
	a.compactor = compactor
}
func (a *optionCaptureAgent) Execute(_ context.Context, _ string, options ...ExecutionOption) (*AgentResult, error) {
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	return &AgentResult{Metadata: map[string]any{"execution_id": cfg.ExecutionID}}, nil
}
func (a *optionCaptureAgent) Reset()               {}
func (a *optionCaptureAgent) GetMemory() []Message { return nil }

func TestAssembleOptionsIncludesExecutionID(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{ID: "agent-1", MaxIterations: 3}}
	_, options, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error: %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if cfg.ExecutionID != "execution-1" {
		t.Fatalf("execution ID not propagated: %q", cfg.ExecutionID)
	}
}

func TestAssembleOptionsPinsKnowledgeExperimentRevisionForTraceAndSearch(t *testing.T) {
	revision := port.KnowledgeRetrievalRevision{
		RevisionID: "knowledge-revision-1", WorkspaceID: "workspace-1", WorkspaceName: "Knowledge One",
		EmbeddingModel: "embedding-1", QueryMode: "hybrid", TopK: 2,
		ScoreThreshold: 0.7, Reranking: "score_desc", QueryRewrite: "lowercase_trim",
	}
	search := &knowledgeRevisionSearchFake{}
	resolver := &knowledgeRevisionResolverFake{assignment: port.KnowledgeRevisionAssignment{
		Revision: revision, ExperimentID: "experiment-1", Variant: "canary",
	}}
	svc := NewAgentService(AgentServiceDeps{
		KnowledgeRevisionResolver: resolver,
		RAGSearch:                 search,
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, KnowledgeWorkspaceIDs: []string{"workspace-1"},
		KnowledgeWorkspaceNames: []string{"Knowledge One"},
	}}
	_, options, err := svc.assembleOptions(context.Background(), agent,
		ExecRequest{ConversationID: "conversation-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatalf("assembleOptions() error: %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if cfg.EvolutionTrace.ResourceManifest["knowledge:Knowledge One"] != "knowledge-revision-1" ||
		cfg.EvolutionTrace.ExperimentAssignments["knowledge:Knowledge One"].Variant != "canary" {
		t.Fatalf("knowledge assignment not traced: %+v", cfg.EvolutionTrace)
	}
	content, err := cfg.RAGSearchFn(context.Background(), []string{"Knowledge One"}, "QUERY", 9)
	if err != nil || content != "canary knowledge" || search.mutableCalls != 0 || search.revisionCalls != 1 ||
		search.revision.RevisionID != revision.RevisionID {
		t.Fatalf("content=%q mutable=%d revision=%d snapshot=%+v err=%v",
			content, search.mutableCalls, search.revisionCalls, search.revision, err)
	}
}

func TestAssembleOptionsClassifiesKnowledgeRevisionSearchFailure(t *testing.T) {
	revision := port.KnowledgeRetrievalRevision{
		RevisionID: "knowledge-revision-1", WorkspaceID: "workspace-1", WorkspaceName: "Knowledge One",
	}
	searchErr := errors.New("vector backend unavailable")
	search := &knowledgeRevisionSearchFake{revisionErr: searchErr}
	svc := NewAgentService(AgentServiceDeps{
		KnowledgeRevisionResolver: &knowledgeRevisionResolverFake{assignment: port.KnowledgeRevisionAssignment{
			Revision: revision, ExperimentID: "experiment-1", Variant: "canary",
		}},
		RAGSearch: search,
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, KnowledgeWorkspaceIDs: []string{"workspace-1"},
		KnowledgeWorkspaceNames: []string{"Knowledge One"},
	}}
	_, options, err := svc.assembleOptions(context.Background(), agent, ExecRequest{ConversationID: "conversation-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatalf("assembleOptions() error: %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	_, err = cfg.RAGSearchFn(context.Background(), []string{"Knowledge One"}, "query", 5)
	if !errors.Is(err, domain.ErrKnowledgeRevisionUnavailable) || !errors.Is(err, searchErr) {
		t.Fatalf("RAGSearchFn() error = %v, want classified revision error wrapping %v", err, searchErr)
	}
	if search.mutableCalls != 0 || search.revisionCalls != 1 {
		t.Fatalf("mutableCalls=%d revisionCalls=%d", search.mutableCalls, search.revisionCalls)
	}
}

func TestAssembleOptionsLoadsPinnedKnowledgeRevisionWithoutReassignment(t *testing.T) {
	revision := port.KnowledgeRetrievalRevision{
		RevisionID: "knowledge-revision-1", WorkspaceID: "workspace-1", WorkspaceName: "Knowledge One",
		EmbeddingModel: "embedding-1", QueryMode: "hybrid", TopK: 2,
		Reranking: "none", QueryRewrite: "none",
	}
	resolver := &knowledgeRevisionResolverFake{assignment: port.KnowledgeRevisionAssignment{Revision: revision}}
	svc := NewAgentService(AgentServiceDeps{KnowledgeRevisionResolver: resolver, RAGSearch: &knowledgeRevisionSearchFake{}})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, KnowledgeWorkspaceIDs: []string{"workspace-1"},
		KnowledgeWorkspaceNames: []string{"Knowledge One"},
	}}
	_, _, err := svc.assembleOptions(context.Background(), agent,
		ExecRequest{ConversationID: "conversation-1"}, ExecMeta{
			TenantID: "tenant-1", TraceID: "trace-1", KnowledgeAssignmentsPinned: true,
			PinnedKnowledgeRevisions: map[string]port.KnowledgeRevisionPin{
				"Knowledge One": {RevisionID: "knowledge-revision-1", ExperimentID: "experiment-1", Variant: "canary"},
			},
		}, "execution-1")
	if err != nil || resolver.assignmentCalls != 0 || resolver.loadCalls != 1 {
		t.Fatalf("assignmentCalls=%d loadCalls=%d err=%v", resolver.assignmentCalls, resolver.loadCalls, err)
	}
}

func TestAssembleOptionsBuildsHistoryCompactorFromTenantGateway(t *testing.T) {
	gateway := capabilityGatewayFake{}
	compactor := historyCompactorFake{}
	var factoryModel string
	svc := NewAgentService(AgentServiceDeps{
		TenantResolver: tenantResolverFake{gateway: gateway},
		HistoryCompactorFactory: func(got port.CapabilityGateway, model string, _ *zap.Logger) port.HistoryCompactor {
			if got != gateway {
				t.Fatalf("factory gateway = %#v, want tenant gateway", got)
			}
			factoryModel = model
			return compactor
		},
	})
	a := &optionCaptureAgent{config: &domain.AgentConfig{ID: "agent-1", LLMModel: "qwen-plus", MaxIterations: 3}}

	if _, _, err := svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	); err != nil {
		t.Fatalf("assembleOptions() error: %v", err)
	}

	if a.gateway != gateway {
		t.Fatal("tenant gateway was not attached")
	}
	if a.compactor != compactor {
		t.Fatal("history compactor was not attached")
	}
	if factoryModel != "qwen-plus" {
		t.Fatalf("factory model = %q", factoryModel)
	}
}

type multiExperimentRevisionResolver struct{}

func (multiExperimentRevisionResolver) ResolveSkillRevision(
	_ context.Context, _, skillID, _ string,
) (port.SkillRevisionAssignment, bool, error) {
	return port.SkillRevisionAssignment{
		RevisionID:   "revision-" + skillID,
		ExperimentID: "experiment-" + skillID,
		Variant:      "canary",
	}, true, nil
}

type multiExperimentActivationResolver struct{}

func (multiExperimentActivationResolver) ResolveSkills(
	_ context.Context, _ string, refs []port.SkillRevisionRef,
) (map[string]port.SkillActivation, error) {
	out := make(map[string]port.SkillActivation, len(refs))
	for _, ref := range refs {
		out[ref.SkillID] = port.SkillActivation{SkillID: ref.SkillID, RevisionID: ref.RevisionID}
	}
	return out, nil
}

type failingExperimentRevisionResolver struct{ err error }

func (f failingExperimentRevisionResolver) ResolveSkillRevision(
	context.Context, string, string, string,
) (port.SkillRevisionAssignment, bool, error) {
	return port.SkillRevisionAssignment{}, false, f.err
}

type failingSkillActivationResolver struct{ err error }

func (f failingSkillActivationResolver) ResolveSkills(
	context.Context, string, []port.SkillRevisionRef,
) (map[string]port.SkillActivation, error) {
	return nil, f.err
}

type agentRevisionResolverFake struct {
	revision domain.AgentRevision
	err      error
}

type mcpRevisionResolverFake struct{}

func (mcpRevisionResolverFake) ResolveMCPRevision(
	_ context.Context, _, serverID, _ string,
) (port.MCPRevisionAssignment, bool, error) {
	return port.MCPRevisionAssignment{
		RevisionID: "revision-" + serverID, ExperimentID: "experiment-" + serverID, Variant: "canary",
	}, true, nil
}

type mcpToolProviderFake struct{}

func (mcpToolProviderFake) ToolsForServer(context.Context, string) []port.ToolDefinition {
	return []port.ToolDefinition{{
		Name: "mcp:server-1:lookup", ProviderType: domain.ProviderTypeMCP,
		ServerID: "server-1", CapabilityID: "lookup",
		InputSchema: map[string]any{"type": "object"}, Metadata: map[string]any{
			"risk_level": "read", "policy_resolved": true,
		},
	}}
}

type revisionCaptureMCPExecutor struct{ revisionID string }

func (e *revisionCaptureMCPExecutor) ExecuteMCPTool(
	context.Context, string, string, map[string]any,
) (port.MCPToolResult, error) {
	return port.MCPToolResult{}, errors.New("mutable MCP execution used")
}

func (e *revisionCaptureMCPExecutor) ExecuteMCPToolRevision(
	_ context.Context, _, _, revisionID string, _ port.ToolRiskLevel, _ map[string]any,
) (port.MCPToolResult, error) {
	e.revisionID = revisionID
	return port.MCPToolResult{StructuredContent: map[string]any{"status": "ok"}}, nil
}

func TestAssembleOptionsPinsMCPExperimentRevisionForTraceAndExecution(t *testing.T) {
	executor := &revisionCaptureMCPExecutor{}
	svc := NewAgentService(AgentServiceDeps{
		MCPRevisionResolver: mcpRevisionResolverFake{}, MCPTools: mcpToolProviderFake{},
		MCPToolExecutor: executor, ToolAuthorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
	})
	a := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, MCPToolIDs: []string{"mcp:server-1:lookup"},
	}}

	_, options, err := svc.assembleOptions(context.Background(), a, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if cfg.EvolutionTrace.ResourceManifest["mcp:server-1"] != "revision-server-1" ||
		cfg.EvolutionTrace.ExperimentAssignments["mcp:server-1"].ExperimentID != "experiment-server-1" {
		t.Fatalf("MCP trace assignment = %#v", cfg.EvolutionTrace)
	}
	_, err = cfg.ToolExecutionFn(context.Background(), port.ToolExecutionRequest{
		ToolCallID: "call-1", Tool: mcpToolProviderFake{}.ToolsForServer(context.Background(), "server-1")[0],
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.revisionID != "revision-server-1" {
		t.Fatalf("executed MCP revision = %q", executor.revisionID)
	}
}

func (f agentRevisionResolverFake) ResolveAgentRevision(
	context.Context, string, string, string,
) (port.AgentRevisionAssignment, bool, error) {
	if f.err != nil {
		return port.AgentRevisionAssignment{}, false, f.err
	}
	return port.AgentRevisionAssignment{
		Revision: f.revision, RevisionID: "agent-revision-canary",
		ExperimentID: "experiment-agent", Variant: "canary",
	}, true, nil
}

func TestResolveExecutionAgentUsesImmutableCanaryRevision(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{AgentRevisionResolver: agentRevisionResolverFake{revision: domain.AgentRevision{
		AgentID: "agent-1", Type: domain.ReActAgent, SystemPrompt: "canary prompt",
		Model: "qwen-plus", MaxIterations: 7,
	}}})
	mutable := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", Type: domain.ReActAgent, SystemPrompt: "mutable prompt",
		LLMModel: "qwen-plus", MaxIterations: 3,
	}}

	resolved, assignment, err := svc.resolveExecutionAgent(
		context.Background(), mutable, "tenant-1", "agent-1", "conversation-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.GetConfig().SystemPrompt != "canary prompt" || resolved.GetConfig().MaxIterations != 7 {
		t.Fatalf("resolved config = %#v", resolved.GetConfig())
	}
	if assignment.RevisionID != "agent-revision-canary" || assignment.ExperimentID != "experiment-agent" ||
		assignment.Variant != "canary" {
		t.Fatalf("assignment = %#v", assignment)
	}
}

func TestResolveExecutionAgentFailsClosed(t *testing.T) {
	wantErr := errors.New("experiment deployment unavailable")
	svc := NewAgentService(AgentServiceDeps{AgentRevisionResolver: agentRevisionResolverFake{err: wantErr}})
	mutable := &optionCaptureAgent{config: &domain.AgentConfig{ID: "agent-1", MaxIterations: 3}}

	_, _, err := svc.resolveExecutionAgent(context.Background(), mutable, "tenant-1", "agent-1", "trace-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("resolveExecutionAgent() error = %v, want %v", err, wantErr)
	}
}

func TestAssembleOptionsFailsClosedWhenExperimentAssignmentFails(t *testing.T) {
	wantErr := errors.New("experiment store unavailable")
	svc := NewAgentService(AgentServiceDeps{
		SkillRevisionResolver: failingExperimentRevisionResolver{err: wantErr},
	})
	a := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, AllowedSkills: []string{"skill-1"},
	}}

	_, _, err := svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("assembleOptions() error=%v want %v", err, wantErr)
	}
}

func TestAssembleOptionsFailsClosedWhenExperimentRevisionLoadFails(t *testing.T) {
	wantErr := errors.New("revision store unavailable")
	svc := NewAgentService(AgentServiceDeps{
		SkillActivationResolver: failingSkillActivationResolver{err: wantErr},
	})
	a := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, AllowedSkills: []string{"skill-1"},
	}}

	_, _, err := svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("assembleOptions() error=%v want %v", err, wantErr)
	}
}

func TestAssembleOptionsAttributesEveryExperimentDeterministically(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		SkillRevisionResolver:   multiExperimentRevisionResolver{},
		SkillActivationResolver: multiExperimentActivationResolver{},
	})
	a := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:            "agent-1",
		MaxIterations: 3,
		AllowedSkills: []string{"skill-b", "skill-a"},
	}}

	_, options, err := svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error: %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	if cfg.EvolutionTrace.ExperimentID != "experiment-skill-b" {
		t.Fatalf("primary experiment must follow allowed skill order: %q", cfg.EvolutionTrace.ExperimentID)
	}
	if len(cfg.EvolutionTrace.ExperimentAssignments) != 2 {
		t.Fatalf("experiment assignments = %#v", cfg.EvolutionTrace.ExperimentAssignments)
	}
	if got := cfg.EvolutionTrace.ExperimentAssignments["skill:skill-a"]; got.ExperimentID != "experiment-skill-a" {
		t.Fatalf("skill-a assignment = %#v", got)
	}
}

func TestAgentServiceListsExecutionsFromEvidenceProviderWithExplicitTenant(t *testing.T) {
	evidence := &evidenceProviderFake{}
	svc := NewAgentService(AgentServiceDeps{EvidenceProvider: evidence})
	rows, total, err := svc.ListExecutions(context.Background(), "tenant-1", "user-1", 1, 20)
	if err != nil {
		t.Fatalf("ListExecutions() error: %v", err)
	}
	if total != 1 || len(rows) != 1 || evidence.tenantID != "tenant-1" || evidence.userID != "user-1" {
		t.Fatalf("rows=%#v total=%d tenant=%q user=%q", rows, total, evidence.tenantID, evidence.userID)
	}
}

func TestAgentServiceHidesTraceDetailsFromAnotherUser(t *testing.T) {
	evidence := &evidenceProviderFake{}
	svc := NewAgentService(AgentServiceDeps{EvidenceProvider: evidence})
	if _, err := svc.ListToolTraces(context.Background(), "tenant-1", "user-2", "trace-1"); !errors.Is(err, domain.ErrEvidenceNotFound) {
		t.Fatalf("cross-user tool traces error = %v", err)
	}
	if _, err := svc.ListTraceEvents(context.Background(), "tenant-1", "user-2", "trace-1"); !errors.Is(err, domain.ErrEvidenceNotFound) {
		t.Fatalf("cross-user trace events error = %v", err)
	}
}

type internalMemoryInjector struct{}

func (internalMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	return "", nil
}

func TestRevisionAgentOnlyInstallsSnapshotRequiredHooks(t *testing.T) {
	recall := port.RecallMemoryFn(func(context.Context, string, string, string, string, map[string]any) (string, error) {
		return "", nil
	})
	svc := NewAgentService(AgentServiceDeps{MemoryInjector: internalMemoryInjector{}, RecallMemory: recall})
	revision := domain.AgentRevision{AgentID: "agent-1", Type: domain.ReActAgent,
		SystemPrompt: "prompt", Model: "model", MaxIterations: 4}

	agent, err := svc.buildRevisionAgent(revision)
	if err != nil {
		t.Fatal(err)
	}
	if agent.MemoryInjector != nil || agent.RecallMemoryFn != nil {
		t.Fatal("hooks not required by snapshot must remain disabled")
	}

	revision.MemoryInjectorRequired, revision.RecallMemoryRequired = true, true
	agent, err = svc.buildRevisionAgent(revision)
	if err != nil {
		t.Fatal(err)
	}
	if agent.MemoryInjector == nil || agent.RecallMemoryFn == nil {
		t.Fatal("required snapshot hooks were not restored")
	}
}

func TestBuildRevisionAgentRejectsCraftedSystemAssistantRevision(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	_, err := svc.buildRevisionAgent(domain.AgentRevision{AgentID: domain.SystemAssistantID})
	if !errors.Is(err, domain.ErrSystemAssistantRevisionUnsupported) {
		t.Fatalf("buildRevisionAgent() error = %v", err)
	}
}

func TestRevisionConfigFiltersKnowledgeMetadataWithDisabledBinding(t *testing.T) {
	revision := domain.AgentRevision{Bindings: []domain.AgentBinding{
		{Kind: domain.AgentBindingKnowledge, ID: "workspace-1", Name: "One", Description: "first", Enabled: true},
		{Kind: domain.AgentBindingKnowledge, ID: "workspace-2", Name: "Two", Description: "second", Enabled: false},
	}}
	cfg := revisionConfig(revision)
	if len(cfg.KnowledgeWorkspaceIDs) != 1 || cfg.KnowledgeWorkspaceIDs[0] != "workspace-1" ||
		len(cfg.KnowledgeWorkspaceNames) != 1 || cfg.KnowledgeWorkspaceNames[0] != "One" ||
		len(cfg.KnowledgeWorkspaceDescriptions) != 1 || cfg.KnowledgeWorkspaceDescriptions[0] != "first" {
		t.Fatalf("disabled knowledge metadata leaked into config: %#v", cfg)
	}
}

package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

func TestParseAgentTypeWireIsCompatibilityOnly(t *testing.T) {
	for _, value := range []string{"react", "planning", "cot", "tool_calling", "rag", "swarm", "legacy"} {
		if got := parseAgentTypeWire(value); got != domain.ReActAgent {
			t.Fatalf("parseAgentTypeWire(%q) = %q, want react", value, got)
		}
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

type evidenceProviderFake struct {
	tenantID string
}

func (f *evidenceProviderFake) ListExecutions(
	_ context.Context, tenantID string, _ domain.ListOptions,
) ([]domain.ExecutionRecord, int64, error) {
	f.tenantID = tenantID
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
	return domain.TraceEvidence{}, nil
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
	_, options := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if cfg.ExecutionID != "execution-1" {
		t.Fatalf("execution ID not propagated: %q", cfg.ExecutionID)
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

	svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)

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

	_, options := svc.assembleOptions(
		context.Background(), a, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
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
	rows, total, err := svc.ListExecutions(context.Background(), "tenant-1", 1, 20)
	if err != nil {
		t.Fatalf("ListExecutions() error: %v", err)
	}
	if total != 1 || len(rows) != 1 || evidence.tenantID != "tenant-1" {
		t.Fatalf("rows=%#v total=%d tenant=%q", rows, total, evidence.tenantID)
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

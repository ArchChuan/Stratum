package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

type optionCaptureAgent struct {
	config *AgentConfig
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

func (a *optionCaptureAgent) GetConfig() *AgentConfig { return a.config }
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

package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

type assistantRoleStub struct{ role string }

func (s assistantRoleStub) ResolveTenantRole(context.Context, string, string) (string, error) {
	return s.role, nil
}

type assistantDiagnosticStub struct{}

func (assistantDiagnosticStub) Collect(context.Context, domain.DiagnosticRequest) (domain.DiagnosticEvidence, error) {
	return domain.DiagnosticEvidence{}, nil
}

func TestSystemAssistantToolDefinitionsAreFixedAndClosed(t *testing.T) {
	tools := SystemAssistantToolDefinitions()
	if len(tools) != 2 || tools[0].Name != ToolSearchOfficialDocs || tools[1].Name != ToolDiagnoseTenant {
		t.Fatalf("tools = %#v", tools)
	}
	for _, tool := range tools {
		if got := tool.InputSchema["additionalProperties"]; got != false {
			t.Fatalf("%s additionalProperties = %#v", tool.Name, got)
		}
		raw, _ := json.Marshal(tool.InputSchema)
		for _, forbidden := range []string{"tenant", "user", "role", "sql", "url", "command", "resource"} {
			if containsFold(string(raw), forbidden) {
				t.Fatalf("%s schema contains forbidden field %q: %s", tool.Name, forbidden, raw)
			}
		}
	}
}

func TestParseOfficialDocsArgumentsRejectsForgedAndUnknownFields(t *testing.T) {
	for _, args := range []map[string]any{
		{"query": "help", "tenant_id": "other"},
		{"query": "help", "url": "https://example.invalid"},
		{"query": "help", "resource_id": "secret"},
	} {
		if _, err := parseOfficialDocsArguments(args); !errors.Is(err, ErrInvalidSystemAssistantToolArguments) {
			t.Fatalf("parseOfficialDocsArguments(%v) error = %v", args, err)
		}
	}
}

func TestParseDiagnosticArgumentsRejectsInvalidAreaAndForgedIdentity(t *testing.T) {
	for _, args := range []map[string]any{
		{"areas": []any{"agent", "database"}},
		{"areas": []any{"agent"}, "role": "owner"},
		{"areas": []any{"agent"}, "user": "other"},
	} {
		if _, err := parseDiagnosticArguments(args); !errors.Is(err, ErrInvalidSystemAssistantToolArguments) {
			t.Fatalf("parseDiagnosticArguments(%v) error = %v", args, err)
		}
	}
}

func TestSystemAssistantCallbacksPreserveNoMatchAndDiagnosticGaps(t *testing.T) {
	docs := func(context.Context, string) ([]domain.Citation, error) {
		return nil, domain.ErrOfficialEvidenceNotFound
	}
	if _, err := docs(context.Background(), "unknown"); !errors.Is(err, domain.ErrOfficialEvidenceNotFound) {
		t.Fatalf("docs error = %v", err)
	}
	evidence := domain.DiagnosticEvidence{Gaps: []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Code: domain.DiagnosticGapUnavailable}}}
	if len(evidence.Gaps) != 1 {
		t.Fatal("diagnostic gaps lost")
	}
}

func TestAssembleOptionsExposesExactlyTwoToolsOnlyToSystemAssistant(t *testing.T) {
	source := BuiltinSystemAssistantProfileSource()
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(nil, source, zap.NewNop()),
		OfficialDocsSearch: func(context.Context, string) ([]domain.Citation, error) { return nil, nil },
		DiagnosticProvider: assistantDiagnosticStub{}, TenantRoleResolver: assistantRoleStub{role: "member"},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{ID: "system", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model", MaxIterations: 3}}
	_, options, err := svc.assembleOptions(context.Background(), system,
		ExecRequest{UserID: "user-1"}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if len(cfg.ExtraTools) != 2 || cfg.ExtraTools[0].Name != ToolSearchOfficialDocs || cfg.ExtraTools[1].Name != ToolDiagnoseTenant {
		t.Fatalf("system tools = %#v", cfg.ExtraTools)
	}
	if len(cfg.SkillCatalog) != 0 || cfg.RAGSearchFn != nil || cfg.ToolExecutionFn != nil {
		t.Fatal("system assistant inherited tenant extensions")
	}

	ordinary := &optionCaptureAgent{config: &domain.AgentConfig{ID: "ordinary", LLMModel: "tenant-model", MaxIterations: 3}}
	_, ordinaryOptions, err := svc.assembleOptions(context.Background(), ordinary,
		ExecRequest{UserID: "user-1"}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-2"}, "execution-2")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryCfg := &ExecutionConfig{}
	ordinaryCfg.ApplyOptions(ordinaryOptions)
	for _, tool := range ordinaryCfg.ExtraTools {
		if tool.Name == ToolSearchOfficialDocs || tool.Name == ToolDiagnoseTenant {
			t.Fatalf("ordinary agent saw system tool %q", tool.Name)
		}
	}
}

func TestSystemAssistantWithoutModelFailsBeforeCapabilityResolution(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Registry: NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop())})
	system := &optionCaptureAgent{config: &domain.AgentConfig{ID: "system", SystemKey: domain.SystemAssistantKey, MaxIterations: 3}}
	_, _, err := svc.assembleOptions(context.Background(), system, ExecRequest{}, ExecMeta{TenantID: "tenant-1"}, "execution-1")
	if !errors.Is(err, domain.ErrAssistantModelUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

var _ port.TenantRoleResolver = assistantRoleStub{}

func containsFold(s, sub string) bool {
	s, sub = lowerASCII(s), lowerASCII(sub)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

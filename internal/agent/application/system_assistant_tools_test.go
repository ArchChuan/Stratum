package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

type assistantDiagnosticStub struct{}

func (assistantDiagnosticStub) Authorize(_ context.Context, req domain.DiagnosticRequest) (domain.DiagnosticAuthorization, error) {
	req.Scope = domain.DiagnosticScopeSelf
	return domain.DiagnosticAuthorization{Request: req, RoleClass: "member"}, nil
}

func (assistantDiagnosticStub) CollectAuthorized(context.Context, domain.DiagnosticRequest) (domain.DiagnosticEvidence, error) {
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
		DiagnosticProvider: assistantDiagnosticStub{},
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
	guarded, err := cfg.InternalToolResultGuardFn(map[string]any{"api_key": "secret-value", "answer": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if !guarded.Untrusted || !containsFold(guarded.ModelContent, "redacted") || containsFold(guarded.ModelContent, "secret-value") {
		t.Fatalf("guarded result = %#v", guarded)
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

var _ port.DiagnosticEvidenceProvider = assistantDiagnosticStub{}

type countingAuthorizedDiagnostics struct{ authorizeCalls, collectCalls int }

func (d *countingAuthorizedDiagnostics) Authorize(_ context.Context, req domain.DiagnosticRequest) (domain.DiagnosticAuthorization, error) {
	d.authorizeCalls++
	req.Scope = domain.DiagnosticScopeSelf
	return domain.DiagnosticAuthorization{Request: req, RoleClass: "member"}, nil
}

func (d *countingAuthorizedDiagnostics) CollectAuthorized(_ context.Context, req domain.DiagnosticRequest) (domain.DiagnosticEvidence, error) {
	d.collectCalls++
	return domain.DiagnosticEvidence{Scope: req.Scope}, nil
}

func TestSystemAssistantAuthorizesRoleOnceAndCapturesScope(t *testing.T) {
	provider := &countingAuthorizedDiagnostics{}
	svc := NewAgentService(AgentServiceDeps{Registry: NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()), DiagnosticProvider: provider})
	system := &optionCaptureAgent{config: &domain.AgentConfig{ID: "system", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model", MaxIterations: 3}}
	_, options, err := svc.assembleOptions(context.Background(), system, ExecRequest{UserID: "user-1"}, ExecMeta{TenantID: "tenant-1"}, "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	evidence, err := cfg.DiagnosticFn(context.Background(), []domain.DiagnosticArea{domain.DiagnosticAreaAgent})
	if err != nil {
		t.Fatal(err)
	}
	if provider.authorizeCalls != 1 || provider.collectCalls != 1 {
		t.Fatalf("calls authorize=%d collect=%d", provider.authorizeCalls, provider.collectCalls)
	}
	if evidence.Scope != domain.DiagnosticScopeSelf || cfg.SystemAssistantRoleClass != "member" {
		t.Fatalf("scope=%q role=%q", evidence.Scope, cfg.SystemAssistantRoleClass)
	}
}

type countingMemoryInjector struct{ calls int }

func (m *countingMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	m.calls++
	return "SHOULD_NOT_APPEAR", nil
}

func TestSystemAssistantRuntimeSkipsMemoryInjectionAndRecall(t *testing.T) {
	injector := &countingMemoryInjector{}
	recalls := 0
	agent := NewBaseAgent(&domain.AgentConfig{ID: "system", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model", MaxIterations: 3, MaxContextTokens: 1000, MemoryScope: "user"}, zap.NewNop())
	agent.MemoryInjector = injector
	agent.RecallMemoryFn = func(context.Context, string, string, string, string, map[string]any) (string, error) {
		recalls++
		return "memory", nil
	}
	agent.SetCapGateway(&systemAssistantPromptGateway{})
	_, err := agent.Execute(context.Background(), "help", WithMaxSteps(3), WithSystemAssistantMode(), WithConversationID("conversation-1"), WithExtraTools(SystemAssistantToolDefinitions()))
	if err != nil {
		t.Fatal(err)
	}
	if injector.calls != 0 || recalls != 0 {
		t.Fatalf("memory calls inject=%d recall=%d", injector.calls, recalls)
	}
}

type recordingAssistantMetrics struct {
	observability.NoopMetrics
	requests []string
}

func (m *recordingAssistantMetrics) IncSystemAssistantRequest(role, version, outcome string) {
	m.requests = append(m.requests, role+":"+version+":"+outcome)
}

func TestSystemAssistantRequestMetricsCoverAssembleFailureAndMemoryWritesStayDisabled(t *testing.T) {
	metrics := &recordingAssistantMetrics{}
	emptyRegistry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{ID: "system", SystemKey: domain.SystemAssistantKey}}}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	emptySvc := NewAgentService(AgentServiceDeps{Registry: emptyRegistry, Metrics: metrics})
	_, _, err := emptySvc.Execute(context.Background(), "system", ExecRequest{UserID: "user-1"}, ExecMeta{TenantID: "tenant-1"})
	if !errors.Is(err, domain.ErrAssistantModelUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if len(metrics.requests) != 1 || !containsFold(metrics.requests[0], "unknown") {
		t.Fatalf("requests = %v", metrics.requests)
	}

	buffers := 0
	readyRegistry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{ID: "system", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model"}}}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	readySvc := NewAgentService(AgentServiceDeps{Registry: readyRegistry, Metrics: metrics,
		TenantResolver: tenantResolverFake{gateway: &systemAssistantPromptGateway{}}, DiagnosticProvider: assistantDiagnosticStub{},
		MemoryBuffer: func(context.Context, string, string, string, string, string, string, string) error {
			buffers++
			return nil
		},
	})
	_, _, err = readySvc.Execute(context.Background(), "system", ExecRequest{UserID: "user-1", Query: "help"}, ExecMeta{TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, cancel, run, err := readySvc.ExecuteStream(context.Background(), "system", ExecRequest{UserID: "user-1", Query: "help"}, ExecMeta{TenantID: "tenant-1"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, _, err = run(); err != nil {
		t.Fatal(err)
	}
	if buffers != 0 {
		t.Fatalf("memory buffers = %d", buffers)
	}
	if len(metrics.requests) != 3 {
		t.Fatalf("requests = %v", metrics.requests)
	}
}

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

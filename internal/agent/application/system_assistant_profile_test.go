package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

func TestComposeSystemAssistantProfileOrdinaryAgentPassthrough(t *testing.T) {
	want := &domain.AgentConfig{
		ID: "agent-1", Name: "Tenant Agent", SystemPrompt: "tenant prompt",
		AllowedSkills: []string{"skill-1"}, MCPToolIDs: []string{"mcp-1"},
		KnowledgeWorkspaceIDs: []string{"knowledge-1"},
	}

	got, err := ComposeSystemAssistantProfile(want, BuiltinSystemAssistantProfile())
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	if got == want {
		t.Fatal("ComposeSystemAssistantProfile() returned the input pointer")
	}
	if got.Name != want.Name || got.SystemPrompt != want.SystemPrompt || len(got.AllowedSkills) != 1 {
		t.Fatalf("ordinary agent changed: got %#v, want %#v", got, want)
	}
}

func TestComposeSystemAssistantProfileOrdinaryAgentCopiesSliceFields(t *testing.T) {
	want := &domain.AgentConfig{
		Capabilities:                   []domain.AgentCapability{{Name: "tenant capability"}},
		KnowledgeWorkspaceNames:        []string{"Knowledge"},
		KnowledgeWorkspaceDescriptions: []string{"description"},
	}

	got, err := ComposeSystemAssistantProfile(want, BuiltinSystemAssistantProfile())
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	got.Capabilities[0].Name = "changed"
	got.KnowledgeWorkspaceNames[0] = "changed"
	got.KnowledgeWorkspaceDescriptions[0] = "changed"
	if want.Capabilities[0].Name == "changed" || want.KnowledgeWorkspaceNames[0] == "changed" ||
		want.KnowledgeWorkspaceDescriptions[0] == "changed" {
		t.Fatal("ordinary agent slice fields alias the persisted config")
	}
}

func TestComposeSystemAssistantProfileReplacesProtectedFieldsAndPreservesTenantRuntimeSelection(t *testing.T) {
	profile := BuiltinSystemAssistantProfile()
	want := &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
		Name: "tenant name", Description: "tenant description", SystemPrompt: "tenant prompt",
		LLMModel: "qwen-plus", EmbedModel: "tenant-embed", MemoryScope: "user",
		MaxIterations: 99, MaxContextTokens: 99999,
		AllowedSkills: []string{"skill-1"}, MCPToolIDs: []string{"mcp-1"},
		KnowledgeWorkspaceIDs:   []string{"knowledge-1"},
		KnowledgeWorkspaceNames: []string{"Knowledge"}, KnowledgeWorkspaceDescriptions: []string{"tenant"},
		Capabilities: []domain.AgentCapability{{Name: "tenant capability"}}, StuckThreshold: 7, CheckpointEnabled: true,
	}

	got, err := ComposeSystemAssistantProfile(want, profile)
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	if got.ID != want.ID || got.LLMModel != want.LLMModel || got.EmbedModel != want.EmbedModel ||
		got.MemoryScope != want.MemoryScope {
		t.Fatalf("tenant runtime selection not preserved: %#v", got)
	}
	if got.Name != profile.Name || got.Description != profile.Description || got.SystemPrompt != profile.SystemPrompt {
		t.Fatalf("protected text fields not replaced: %#v", got)
	}
	if got.MaxIterations != profile.MaxIterations || got.MaxContextTokens != profile.MaxContextTokens {
		t.Fatalf("protected budgets not replaced: %#v", got)
	}
	if got.SystemKey != profile.Key || !got.IsSystem || got.ManagementMode != "platform" {
		t.Fatalf("managed identity not composed: %#v", got)
	}
	if len(got.AllowedSkills) != 0 || len(got.MCPToolIDs) != 0 || len(got.KnowledgeWorkspaceIDs) != 0 ||
		len(got.KnowledgeWorkspaceNames) != 0 || len(got.KnowledgeWorkspaceDescriptions) != 0 ||
		len(got.Capabilities) != 0 || got.StuckThreshold != 0 || got.CheckpointEnabled {
		t.Fatalf("tenant extensions survived composition: %#v", got)
	}
}

func TestComposeSystemAssistantProfileFailsClosedForInvalidProfile(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *domain.AgentConfig
		profile *domain.SystemAssistantProfile
	}{
		{name: "nil config", profile: BuiltinSystemAssistantProfile()},
		{name: "nil profile", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey}},
		{
			name: "unknown key", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey},
			profile: &domain.SystemAssistantProfile{Key: "unknown", Version: "2026-07-23.v1"},
		},
		{
			name: "unknown version", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey},
			profile: &domain.SystemAssistantProfile{Key: domain.SystemAssistantKey, Version: "unknown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ComposeSystemAssistantProfile(tt.cfg, tt.profile); err == nil {
				t.Fatal("ComposeSystemAssistantProfile() error = nil")
			}
		})
	}
}

func TestComposeSystemAssistantProfileUsesCodeReviewedVersionInsteadOfMutableInput(t *testing.T) {
	profile := BuiltinSystemAssistantProfile()
	profile.Name = "tampered name"
	profile.SystemPrompt = "tampered prompt"

	got, err := ComposeSystemAssistantProfile(&domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
	}, profile)
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	want := BuiltinSystemAssistantProfile()
	if got.Name != want.Name || got.SystemPrompt != want.SystemPrompt {
		t.Fatalf("mutable profile input reached runtime: %#v", got)
	}
}

type systemAssistantProfileRepo struct {
	cfgs []*domain.AgentConfig
	err  error
}

func (r systemAssistantProfileRepo) Register(context.Context, *domain.AgentConfig) error { return nil }
func (r systemAssistantProfileRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	if len(r.cfgs) == 0 {
		return nil, false, nil
	}
	return r.cfgs[0], true, nil
}
func (r systemAssistantProfileRepo) GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error) {
	return r.Get(ctx, domain.SystemAssistantKey)
}
func (r systemAssistantProfileRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return r.cfgs, r.err
}
func (r systemAssistantProfileRepo) Update(context.Context, *domain.AgentConfig) error { return nil }
func (r systemAssistantProfileRepo) UpdateSystemAssistantModel(context.Context, string) error {
	return nil
}
func (r systemAssistantProfileRepo) Remove(context.Context, string) error { return nil }

var _ port.AgentRepo = systemAssistantProfileRepo{}

type systemAssistantPromptGateway struct {
	request port.CapabilityRequest
}

func (g *systemAssistantPromptGateway) Route(
	_ context.Context, request port.CapabilityRequest,
) (port.CapabilityResponse, error) {
	g.request = request
	return port.CapabilityResponse{Content: "done"}, nil
}

func TestSystemAssistantProfileRegistryPropagatesRepositoryAndCompositionFailures(t *testing.T) {
	wantErr := errors.New("repository unavailable")
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	if _, _, err := registry.Get(context.Background(), "assistant-1"); !errors.Is(err, wantErr) {
		t.Fatalf("Registry.Get() error = %v, want %v", err, wantErr)
	}
	if _, err := registry.GetAll(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Registry.GetAll() error = %v, want %v", err, wantErr)
	}

	registry = NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
	}}}, nil, zap.NewNop())
	if _, _, err := registry.Get(context.Background(), "assistant-1"); err == nil {
		t.Fatal("Registry.Get() composition error = nil")
	}
	if _, err := registry.GetAll(context.Background()); err == nil {
		t.Fatal("Registry.GetAll() composition error = nil")
	}
}

func TestSystemAssistantProfileAgentServicePropagatesRegistryFailures(t *testing.T) {
	wantErr := errors.New("repository unavailable")
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := NewAgentService(AgentServiceDeps{Registry: registry})

	if _, err := svc.Get(context.Background(), "assistant-1"); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.Get() error = %v, want %v", err, wantErr)
	}
	if _, err := svc.List(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.List() error = %v, want %v", err, wantErr)
	}
}

func TestSystemAssistantProfileVersionRecordedInTraceMetadata(t *testing.T) {
	source := BuiltinSystemAssistantProfileSource()
	svc := NewAgentService(AgentServiceDeps{Registry: NewRegistry(nil, source, zap.NewNop())})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey, MaxIterations: 3,
	}}

	_, options, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error = %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if got := cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"]; got != source.Version() {
		t.Fatalf("profile version = %q, want %q", got, source.Version())
	}
}

func TestSystemAssistantProfileTraceFailsClosedWithoutSharedSource(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Registry: NewRegistry(nil, nil, zap.NewNop())})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey, MaxIterations: 3,
	}}

	if _, _, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	); err == nil {
		t.Fatal("assembleOptions() error = nil without shared profile source")
	}
}

func TestSystemAssistantProfileManagedRuntimeDoesNotAppendGlobalSuffix(t *testing.T) {
	source, err := NewBuiltinSystemAssistantProfileSource(domain.CurrentSystemAssistantProfileVersion)
	if err != nil {
		t.Fatalf("NewBuiltinSystemAssistantProfileSource() error = %v", err)
	}
	registry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "assistant-1", SystemKey: domain.SystemAssistantKey},
	}}, source, zap.NewNop())
	registry.SetGlobalSystemSuffix("tenant-global-suffix")

	agent, found, err := registry.Get(context.Background(), "assistant-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	base := agent.(*BaseAgent)
	if base.GlobalSystemSuffix != "" {
		t.Fatalf("managed runtime suffix = %q, want empty", base.GlobalSystemSuffix)
	}
	managedGateway := &systemAssistantPromptGateway{}
	base.SetCapGateway(managedGateway)
	if _, err := base.Execute(context.Background(), "help"); err != nil {
		t.Fatalf("managed Execute() error = %v", err)
	}
	if got := managedGateway.request.LLM.Messages[0].Content; strings.Contains(got, "tenant-global-suffix") {
		t.Fatalf("managed effective prompt contains global suffix: %q", got)
	}

	registry = NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "agent-1", SystemPrompt: "tenant prompt"},
	}}, source, zap.NewNop())
	registry.SetGlobalSystemSuffix("tenant-global-suffix")
	agent, found, err = registry.Get(context.Background(), "agent-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	if got := agent.(*BaseAgent).GlobalSystemSuffix; got != "tenant-global-suffix" {
		t.Fatalf("ordinary runtime suffix = %q", got)
	}
	ordinary := agent.(*BaseAgent)
	ordinaryGateway := &systemAssistantPromptGateway{}
	ordinary.SetCapGateway(ordinaryGateway)
	if _, err := ordinary.Execute(context.Background(), "help"); err != nil {
		t.Fatalf("ordinary Execute() error = %v", err)
	}
	if got := ordinaryGateway.request.LLM.Messages[0].Content; !strings.Contains(got, "tenant-global-suffix") {
		t.Fatalf("ordinary effective prompt omits global suffix: %q", got)
	}
}

func TestSystemAssistantProfileRollbackSourceKeepsRuntimeAndTraceOnSameImmutableVersion(t *testing.T) {
	const rollbackVersion = "2026-07-22.v0"
	source, err := NewBuiltinSystemAssistantProfileSource(rollbackVersion)
	if err != nil {
		t.Fatalf("NewBuiltinSystemAssistantProfileSource() error = %v", err)
	}
	snapshot := source.Profile()
	snapshot.Version = "mutated"
	snapshot.SystemPrompt = "mutated prompt"

	registry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "assistant-1", SystemKey: domain.SystemAssistantKey},
	}}, source, zap.NewNop())
	agent, found, err := registry.Get(context.Background(), "assistant-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	if agent.GetConfig().SystemPrompt == "mutated prompt" {
		t.Fatal("caller mutation changed composed runtime prompt")
	}

	svc := NewAgentService(AgentServiceDeps{Registry: registry})
	_, options, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error = %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if got := cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"]; got != rollbackVersion {
		t.Fatalf("trace profile version = %q, runtime source version = %q", got, rollbackVersion)
	}
}

package application

import (
	"context"
	"errors"
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

func TestSystemAssistantProfileRegistryPropagatesRepositoryAndCompositionFailures(t *testing.T) {
	wantErr := errors.New("repository unavailable")
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfile(), zap.NewNop())
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
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfile(), zap.NewNop())
	svc := NewAgentService(AgentServiceDeps{Registry: registry})

	if _, err := svc.Get(context.Background(), "assistant-1"); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.Get() error = %v, want %v", err, wantErr)
	}
	if _, err := svc.List(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.List() error = %v, want %v", err, wantErr)
	}
}

func TestSystemAssistantProfileVersionRecordedInTraceMetadata(t *testing.T) {
	profile := BuiltinSystemAssistantProfile()
	svc := NewAgentService(AgentServiceDeps{SystemAssistantProfile: profile})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey, MaxIterations: 3,
	}}

	_, options := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if got := cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"]; got != profile.Version {
		t.Fatalf("profile version = %q, want %q", got, profile.Version)
	}
}

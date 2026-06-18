package handler

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"go.uber.org/zap"
)

func newTestMCPToolProvider(t *testing.T) (port.MCPToolProvider, *mcp.MCPSkillRegistry) {
	t.Helper()
	registry := mcp.NewMCPSkillRegistry(nil, zap.NewNop())
	return mcp.RegistryAsAgentToolProvider(registry), registry
}

func injectMCPTool(t *testing.T, registry *mcp.MCPSkillRegistry, serverID string, id, name, desc string) {
	t.Helper()
	adapter := mcp.NewMCPSkillAdapter(serverID, nil, zap.NewNop())
	adapter.AddSkillForTest(&mcp.MCPSkillWrapper{
		ID:   id,
		Name: name,
		Tool: &mcp.MCPTool{Name: name, Description: desc, InputSchema: map[string]interface{}{"type": "object"}},
	})
	registry.RegisterAdapterForTest(serverID, adapter)
}

func TestBuildExtraTools_EmptyInputs(t *testing.T) {
	h := &AgentHandler{}
	if got := h.buildExtraTools(context.Background(), nil, nil); len(got) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(got))
	}
}

func TestBuildExtraTools_NilMCPRegistry(t *testing.T) {
	h := &AgentHandler{mcpToolProvider: nil}
	tools := h.buildExtraTools(context.Background(), []string{"srv1"}, nil)
	if len(tools) != 0 {
		t.Fatalf("nil mcpToolProvider: expected 0, got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPServerNotRegistered(t *testing.T) {
	provider, _ := newTestMCPToolProvider(t)
	h := &AgentHandler{mcpToolProvider: provider}
	tools := h.buildExtraTools(context.Background(), []string{"ghost"}, nil)
	if len(tools) != 0 {
		t.Fatalf("unregistered server: expected 0, got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPSkillExpanded(t *testing.T) {
	provider, registry := newTestMCPToolProvider(t)
	injectMCPTool(t, registry, "srv1", "mcp:srv1:search", "search", "web search")

	h := &AgentHandler{mcpToolProvider: provider}
	tools := h.buildExtraTools(context.Background(), []string{"srv1"}, nil)

	if len(tools) != 1 {
		t.Fatalf("expected 1 MCP tool, got %d", len(tools))
	}
	td := tools[0]
	if td.Name != "mcp:srv1:search" {
		t.Errorf("name: got %q, want mcp:srv1:search", td.Name)
	}
	if td.Description != "web search" {
		t.Errorf("description: got %q, want 'web search'", td.Description)
	}
	if td.InputSchema == nil {
		t.Error("InputSchema must not be nil")
	}
}

func TestBuildExtraTools_SkillFallback_NilDB(t *testing.T) {
	h := &AgentHandler{}
	tools := h.buildExtraTools(context.Background(), nil, []string{"my-skill"})

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	td := tools[0]
	if td.Name != "my-skill" {
		t.Errorf("name: got %q, want my-skill", td.Name)
	}
	want := "my-skill: my-skill"
	if td.Description != want {
		t.Errorf("description: got %q, want %q", td.Description, want)
	}
	if td.InputSchema == nil {
		t.Error("InputSchema must not be nil")
	}
}

func TestBuildExtraTools_MultipleSkills_Fallback(t *testing.T) {
	h := &AgentHandler{}
	tools := h.buildExtraTools(context.Background(), nil, []string{"skill-a", "skill-b"})
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, td := range tools {
		names[td.Name] = true
	}
	for _, id := range []string{"skill-a", "skill-b"} {
		if !names[id] {
			t.Errorf("missing tool %q in results", id)
		}
	}
}

func TestBuildExtraTools_Combined_MCPAndSkill(t *testing.T) {
	provider, registry := newTestMCPToolProvider(t)
	injectMCPTool(t, registry, "s1", "mcp:s1:calc", "calc", "a calculator")

	h := &AgentHandler{mcpToolProvider: provider}
	tools := h.buildExtraTools(context.Background(), []string{"s1"}, []string{"send-email"})

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (1 MCP + 1 skill), got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPMultipleTools(t *testing.T) {
	provider, registry := newTestMCPToolProvider(t)
	adapter := mcp.NewMCPSkillAdapter("multi", nil, zap.NewNop())
	for _, name := range []string{"tool1", "tool2", "tool3"} {
		adapter.AddSkillForTest(&mcp.MCPSkillWrapper{
			ID:   "mcp:multi:" + name,
			Name: name,
			Tool: &mcp.MCPTool{Name: name, Description: name + " desc"},
		})
	}
	registry.RegisterAdapterForTest("multi", adapter)

	h := &AgentHandler{mcpToolProvider: provider}
	tools := h.buildExtraTools(context.Background(), []string{"multi"}, nil)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
}

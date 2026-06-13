package handler

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp"
	"go.uber.org/zap"
)

func newTestMCPRegistry(t *testing.T) *mcp.MCPSkillRegistry {
	t.Helper()
	return mcp.NewMCPSkillRegistry(nil, zap.NewNop())
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
	if got := h.buildExtraTools(nil, nil); len(got) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(got))
	}
}

func TestBuildExtraTools_NilMCPRegistry(t *testing.T) {
	h := &AgentHandler{mcpRegistry: nil}
	tools := h.buildExtraTools([]string{"srv1"}, nil)
	if len(tools) != 0 {
		t.Fatalf("nil mcpRegistry: expected 0, got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPServerNotRegistered(t *testing.T) {
	h := &AgentHandler{mcpRegistry: newTestMCPRegistry(t)}
	tools := h.buildExtraTools([]string{"ghost"}, nil)
	if len(tools) != 0 {
		t.Fatalf("unregistered server: expected 0, got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPSkillExpanded(t *testing.T) {
	registry := newTestMCPRegistry(t)
	injectMCPTool(t, registry, "srv1", "mcp:srv1:search", "search", "web search")

	h := &AgentHandler{mcpRegistry: registry}
	tools := h.buildExtraTools([]string{"srv1"}, nil)

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

func TestBuildExtraTools_SkillFallback_NilRegistry(t *testing.T) {
	h := &AgentHandler{skillRegistry: nil}
	tools := h.buildExtraTools(nil, []string{"my-skill"})

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
	tools := h.buildExtraTools(nil, []string{"skill-a", "skill-b"})
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
	registry := newTestMCPRegistry(t)
	injectMCPTool(t, registry, "s1", "mcp:s1:calc", "calc", "a calculator")

	h := &AgentHandler{mcpRegistry: registry}
	tools := h.buildExtraTools([]string{"s1"}, []string{"send-email"})

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (1 MCP + 1 skill), got %d", len(tools))
	}
}

func TestBuildExtraTools_MCPMultipleTools(t *testing.T) {
	registry := newTestMCPRegistry(t)
	adapter := mcp.NewMCPSkillAdapter("multi", nil, zap.NewNop())
	for _, name := range []string{"tool1", "tool2", "tool3"} {
		adapter.AddSkillForTest(&mcp.MCPSkillWrapper{
			ID:   "mcp:multi:" + name,
			Name: name,
			Tool: &mcp.MCPTool{Name: name, Description: name + " desc"},
		})
	}
	registry.RegisterAdapterForTest("multi", adapter)

	h := &AgentHandler{mcpRegistry: registry}
	tools := h.buildExtraTools([]string{"multi"}, nil)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
}

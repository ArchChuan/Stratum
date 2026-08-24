package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"go.uber.org/zap"
)

// TestAssembleOptionsRetainsPlatformBindings：平台助手与普通 agent 等同化后，
// builtin skill / platform MCP server / platform workspace 对普通 agent 开放
// 挂载。装配不再净化剔除：config 原样保留，platform MCP 工具进入执行白名单。
func TestAssembleOptionsRetainsPlatformBindings(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		MCPTools:             genericMCPTools{},
		Registry:             NewRegistry(nil, zap.NewNop()),
		TenantModelValidator: &stubTenantModelValidator{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3, LLMModel: "test-model",
		AllowedSkills:         []string{"builtin:platform-guide", "custom-skill"},
		MCPToolIDs:            []string{"mcp:orders:get"},
		KnowledgeWorkspaceIDs: []string{"platform-ws"},
	}}
	_, options, err := svc.assembleOptions(context.Background(), agent, ExecRequest{},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatalf("ordinary agent with platform bindings must assemble, got %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if len(agent.config.AllowedSkills) != 2 || len(agent.config.MCPToolIDs) != 1 ||
		len(agent.config.KnowledgeWorkspaceIDs) != 1 {
		t.Fatalf("platform bindings were stripped: %#v", agent.config)
	}
	// platform MCP 工具原样进入执行白名单（不再被净化剔除）。
	found := false
	for _, def := range cfg.ExtraTools {
		if def.Name == "mcp:orders:get" {
			found = true
		}
	}
	if !found {
		t.Fatalf("platform mcp tool missing from extra tools: %#v", cfg.ExtraTools)
	}
}

// TestAssembleOptionsRAGClosureSearchesPlatformWorkspace：开放挂载后 platform
// workspace 可被普通 agent 挂载检索，RAG 闭包不再交集剔除。
func TestAssembleOptionsRAGClosureSearchesPlatformWorkspace(t *testing.T) {
	search := &knowledgeRevisionSearchFake{}
	svc := NewAgentService(AgentServiceDeps{
		// 不配 KnowledgeRevisionResolver：knowledgeAssignments 为空，闭包对
		// mutable 只记录实际传入的 workspaces。
		RAGSearch:            search,
		TenantModelValidator: &stubTenantModelValidator{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3,
		LLMModel:                "test-model",
		KnowledgeWorkspaceIDs:   []string{"platform-ws", "normal-ws"},
		KnowledgeWorkspaceNames: []string{"platform docs", "normal docs"},
	}}
	_, options, err := svc.assembleOptions(context.Background(), agent, ExecRequest{ConversationID: "conversation-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if _, err := cfg.RAGSearchFn(context.Background(), []string{"platform docs", "normal docs"}, "query", 5, "viewer-1"); err == nil {
		t.Fatal("mutable search fake must error on use; if it did not run, search was skipped")
	}
	if search.mutableCalls != 1 {
		t.Fatalf("mutableCalls = %d, want 1", search.mutableCalls)
	}
	// 开放挂载后 platform workspace 与普通 workspace 一起进入检索列表。
	if len(search.mutableWorkspaces) != 2 || search.mutableWorkspaces[0] != "platform docs" {
		t.Fatalf("mutable workspaces = %#v, platform workspace must be searchable", search.mutableWorkspaces)
	}
}

package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

func TestSanitizeRuntimeBindingsStripsPlatformBindings(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		SystemResourceGuard: stubSystemResourceGuard{
			platformMCP: []string{"platform-server"},
			platformWS:  []string{"platform-ws"},
		},
		Logger: zap.NewNop(),
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:                      "agent-1",
		AllowedSkills:           []string{"builtin:platform-guide", "custom-skill"},
		MCPToolIDs:              []string{"mcp:platform-server:tool-a", "mcp:user-server:tool-b", "malformed"},
		KnowledgeWorkspaceIDs:   []string{"platform-ws", "normal-ws", "extra-id"},
		KnowledgeWorkspaceNames: []string{"platform docs", "normal docs"},
		// 描述组短于 IDs:索引越界时必须保留其余索引,不得 panic 或错位。
		KnowledgeWorkspaceDescriptions: []string{"platform desc"},
	}}
	removed, err := svc.sanitizeRuntimeBindings(context.Background(), "tenant-1", agent)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agent.config
	if len(cfg.AllowedSkills) != 1 || cfg.AllowedSkills[0] != "custom-skill" {
		t.Fatalf("allowed skills = %#v", cfg.AllowedSkills)
	}
	if len(cfg.MCPToolIDs) != 2 || cfg.MCPToolIDs[0] != "mcp:user-server:tool-b" || cfg.MCPToolIDs[1] != "malformed" {
		t.Fatalf("mcp tool ids = %#v", cfg.MCPToolIDs)
	}
	if len(cfg.KnowledgeWorkspaceIDs) != 2 || cfg.KnowledgeWorkspaceIDs[0] != "normal-ws" || cfg.KnowledgeWorkspaceIDs[1] != "extra-id" {
		t.Fatalf("knowledge workspace ids = %#v", cfg.KnowledgeWorkspaceIDs)
	}
	if len(cfg.KnowledgeWorkspaceNames) != 1 || cfg.KnowledgeWorkspaceNames[0] != "normal docs" {
		t.Fatalf("knowledge workspace names = %#v", cfg.KnowledgeWorkspaceNames)
	}
	if len(cfg.KnowledgeWorkspaceDescriptions) != 0 {
		t.Fatalf("knowledge workspace descriptions = %#v", cfg.KnowledgeWorkspaceDescriptions)
	}
	if len(removed) != 1 || removed[0] != "platform docs" {
		t.Fatalf("removed workspace names = %#v", removed)
	}
}

func TestSanitizeRuntimeBindingsZeroBindingsSkipsGuard(t *testing.T) {
	// 零绑定短路:即使 guard 查询必然失败也不触发查询(fail-closed 语义只
	// 对非空绑定生效),普通 agent 无内置资源时装配不被拖垮。
	svc := NewAgentService(AgentServiceDeps{
		SystemResourceGuard: stubSystemResourceGuard{mcpErr: errors.New("guard must not be consulted")},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{ID: "agent-1", MaxIterations: 3}}
	if _, err := svc.sanitizeRuntimeBindings(context.Background(), "tenant-1", agent); err != nil {
		t.Fatalf("zero-binding agent must short-circuit guard, got %v", err)
	}
}

func TestSanitizeRuntimeBindingsNilGuardFailClosed(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", AllowedSkills: []string{"builtin:platform-guide"},
	}}
	if _, err := svc.sanitizeRuntimeBindings(context.Background(), "tenant-1", agent); err == nil {
		t.Fatal("nil guard with non-empty bindings must fail closed")
	}
}

func TestSanitizeRuntimeBindingsQueryFailureFailClosed(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		SystemResourceGuard: stubSystemResourceGuard{wsErr: errors.New("workspace store unavailable")},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", KnowledgeWorkspaceIDs: []string{"workspace-1"},
	}}
	if _, err := svc.sanitizeRuntimeBindings(context.Background(), "tenant-1", agent); err == nil {
		t.Fatal("platform workspace query failure must fail closed")
	}
}

// TestAssembleOptionsRAGClosureReintersectsPlatformWorkspace(E4 兜底):净化后
// platform workspace 已从 config 剔除;即便 workspaces 参数仍含 platform
// workspace(某些路径绕过装配),RAG 闭包再交集也保证它不进入 SearchKnowledge。
func TestAssembleOptionsRAGClosureReintersectsPlatformWorkspace(t *testing.T) {
	search := &knowledgeRevisionSearchFake{}
	svc := NewAgentService(AgentServiceDeps{
		// 不配 KnowledgeRevisionResolver:净化后剩余 normal-ws 无 experiment
		// assignment,knowledgeAssignments 为空,闭包对 mutable 只做再交集断言。
		RAGSearch: search,
		SystemResourceGuard: stubSystemResourceGuard{
			platformWS: []string{"platform-ws"},
		},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3,
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

	// 模拟某条路径把 platform workspace 名又塞回 workspaces 参数。
	if _, err := cfg.RAGSearchFn(context.Background(), []string{"platform docs", "normal docs"}, "query", 5, "viewer-1"); err == nil {
		t.Fatal("mutable search fake must error on use; if it did not run, the intersection failed open")
	}
	if search.mutableCalls != 1 {
		t.Fatalf("mutableCalls = %d, want 1", search.mutableCalls)
	}
	if len(search.mutableWorkspaces) != 1 || search.mutableWorkspaces[0] != "normal docs" {
		t.Fatalf("mutable workspaces = %#v, platform workspace leaked", search.mutableWorkspaces)
	}
}

// TestAssembleOptionsSanitizesMCPToolIDsForExecution(M3):净化后 AgentToolIDs
// 读点(:2022 闭包内 a.GetConfig().MCPToolIDs)读到的是不含 platform server
// 工具的净化值,platform MCP 工具不会进入执行白名单。
func TestAssembleOptionsSanitizesMCPToolIDsForExecution(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		MCPTools: genericMCPTools{},
		SystemResourceGuard: stubSystemResourceGuard{
			platformMCP: []string{"orders"},
		},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "agent-1", MaxIterations: 3,
		MCPToolIDs: []string{"mcp:orders:get", "mcp:orders:list"},
	}}
	if _, err := svc.sanitizeRuntimeBindings(context.Background(), "tenant-1", agent); err != nil {
		t.Fatal(err)
	}
	if len(agent.config.MCPToolIDs) != 0 {
		t.Fatalf("sanitized mcp tool ids = %#v, platform server tools leaked", agent.config.MCPToolIDs)
	}
}

// TestAssembleOptionsSystemAssistantKeepsPlatformBindings:系统助手不经净化,
// guard 即使不可用也不被触碰(config 保留 platform 绑定)。
func TestAssembleOptionsSystemAssistantKeepsPlatformBindings(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: &strictModelValidatorStub{},
		SystemResourceGuard:  stubSystemResourceGuard{mcpErr: errors.New("guard must not be consulted")},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus", MaxIterations: 3,
		AllowedSkills:         []string{"builtin:platform-guide"},
		MCPToolIDs:            []string{"mcp:platform-server:lookup"},
		KnowledgeWorkspaceIDs: []string{"platform-ws"},
	}}
	if _, _, err := svc.assembleOptions(context.Background(), system, ExecRequest{},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1"); err != nil {
		t.Fatalf("system assistant must skip sanitization, got %v", err)
	}
	if len(system.config.AllowedSkills) != 1 || len(system.config.MCPToolIDs) != 1 ||
		len(system.config.KnowledgeWorkspaceIDs) != 1 {
		t.Fatalf("system assistant bindings were stripped: %#v", system.config)
	}
}

// 编译期:stubSystemResourceGuard 实现 SystemResourceGuard 全接口。
var _ port.SystemResourceGuard = stubSystemResourceGuard{}

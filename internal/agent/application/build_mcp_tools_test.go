package application

import (
	"context"
	"testing"

	port "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type emptyMCPToolProvider struct{}

func (emptyMCPToolProvider) ToolsForServer(context.Context, string, string) []port.ToolDefinition {
	return nil
}

type fixedMCPToolProvider struct{ tools []port.ToolDefinition }

func (p fixedMCPToolProvider) ToolsForServer(context.Context, string, string) []port.ToolDefinition {
	return p.tools
}

// TestBuildMCPToolsWarnsWhenBoundToolsNotExposed 验证防御第二层：agent 绑定了
// MCP 工具但最终一个都没暴露（catalog 缺失/服务端未返回/不匹配）时显式告警，
// 不再静默 drop —— 此前远端故障表现为"模型无 MCP 工具可调用"却无任何日志。
func TestBuildMCPToolsWarnsWhenBoundToolsNotExposed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider port.MCPToolProvider
		bound    []string
	}{
		{
			name:     "catalog empty",
			provider: emptyMCPToolProvider{},
			bound:    []string{"mcp:orders:get_order"},
		},
		{
			name:     "provider returns unmatched tool",
			provider: fixedMCPToolProvider{tools: []port.ToolDefinition{{Name: "mcp:orders:other"}}},
			bound:    []string{"mcp:orders:get_order"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.WarnLevel)
			svc := NewAgentService(AgentServiceDeps{
				Logger:   zap.New(core),
				MCPTools: tc.provider,
			})

			tools := svc.buildMCPTools(context.Background(), "t1", tc.bound)

			require.Empty(t, tools)
			warns := logs.FilterMessage("agent bound MCP tools but none exposed")
			require.Equal(t, 1, warns.Len(), "missing-tool condition must be logged explicitly")
			entry := warns.All()[0]
			require.Equal(t, "t1", entry.ContextMap()["tenant_id"])
			bound, ok := entry.ContextMap()["bound_tool_ids"].([]interface{})
			require.True(t, ok, "bound_tool_ids must be a list")
			require.Len(t, bound, 1)
			require.Equal(t, "mcp:orders:get_order", bound[0])
		})
	}
}

// TestBuildMCPToolsExposesBoundTools 验证正向路径：provider 返回绑定 ID 对应的
// 工具时正常暴露（含默认 MCP 元数据），且不产生缺失告警。
func TestBuildMCPToolsExposesBoundTools(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	svc := NewAgentService(AgentServiceDeps{
		Logger: zap.New(core),
		MCPTools: fixedMCPToolProvider{tools: []port.ToolDefinition{
			{Name: "mcp:orders:get_order", CapabilityID: "get_order", Description: "look up an order"},
		}},
	})

	tools := svc.buildMCPTools(context.Background(), "t1", []string{"mcp:orders:get_order"})

	require.Len(t, tools, 1)
	require.Equal(t, "mcp:orders:get_order", tools[0].Name)
	require.Equal(t, "orders", tools[0].ServerID)
	require.Equal(t, "get_order", tools[0].CapabilityID)
	require.Zero(t, logs.FilterMessage("agent bound MCP tools but none exposed").Len(),
		"healthy exposure must not warn")
}

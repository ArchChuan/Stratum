package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSupportsResourcesCapability(t *testing.T) {
	cases := []struct {
		name string
		init *mcp.InitializeResult
		want bool
	}{
		{name: "nil initialize result", init: nil, want: false},
		{name: "no capabilities declared", init: &mcp.InitializeResult{Capabilities: nil}, want: false},
		{
			name: "tools only",
			init: &mcp.InitializeResult{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}},
			want: false,
		},
		{
			name: "tools and resources",
			init: &mcp.InitializeResult{Capabilities: &mcp.ServerCapabilities{
				Tools:     &mcp.ToolCapabilities{},
				Resources: &mcp.ResourceCapabilities{},
			}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, supportsResourcesCapability(tc.init))
		})
	}
}

// newToolsOnlyServer 启动一个只声明 tools 能力的官方 SDK MCP server，并对
// resources/list 返回 JSON-RPC 错误——复现百炼等托管 MCP 的行为：initialize
// 与 tools/list 正常，resources/list 未实现。修复前资源发现失败会拖垮整个
// 连接（discover MCP resources: MCP transport failed）。
func newToolsOnlyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "tools-only", Version: "1.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "echoes the text argument",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, readErr := io.ReadAll(r.Body)
			if readErr == nil {
				var req struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.Unmarshal(body, &req) == nil && req.Method == "resources/list" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(fmt.Sprintf(
						`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`,
						string(req.ID))))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestConnectToolsOnlyServerSkipsResourceDiscovery 验证 manager 连接只声明
// tools 的服务器时不再调用 resources/list，连接与工具发现照常成功。
func TestConnectToolsOnlyServerSkipsResourceDiscovery(t *testing.T) {
	ts := newToolsOnlyServer(t)
	manager := NewClientManager(zap.NewNop(), nil, nil)
	manager.WithURLPolicy(URLPolicyAllowPrivate)

	cfg := &MCPServerConfig{
		ID:        "tools-only",
		Name:      "Tools Only",
		Transport: "http",
		URL:       ts.URL,
	}
	require.NoError(t, manager.Connect(context.Background(), cfg, nil, "", nil))
	t.Cleanup(func() {
		_ = manager.Disconnect(context.Background(), "tools-only")
	})

	tools, err := manager.ListTools(context.Background(), "tools-only")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "echo", tools[0].Name)

	resources, err := manager.ListResources(context.Background(), "tools-only")
	require.NoError(t, err)
	require.Len(t, resources, 0)
}

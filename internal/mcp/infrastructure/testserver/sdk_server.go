package testserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SDKServer is an MCP server backed by the official SDK (mcp.NewServer +
// NewStreamableHTTPHandler). Tests that exercise the real SDK client handshake
// (initialize → standalone SSE → tools/list → tools/call) must point at this
// fixture; the legacy fake_server speaks a hand-rolled JSON-RPC dialect the
// SDK client rejects.
type SDKServer struct {
	server *httptest.Server
}

// NewSDKServer starts an SDK server exposing the given tools. Each tool call
// returns an empty text result; tests that need behavior register their own
// handlers via AddToolHandler.
func NewSDKServer(t testing.TB, tools []Tool) *SDKServer {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "stratum-test", Version: "1.0"}, nil)
	for _, tool := range tools {
		tool := tool
		srv.AddTool(&mcp.Tool{
			Name:         tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Annotations:  annotations(tool.Annotations),
		}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	s := &SDKServer{server: httptest.NewServer(handler)}
	t.Cleanup(s.Close)
	return s
}

func (s *SDKServer) URL() string { return s.server.URL }
func (s *SDKServer) Close()      { s.server.Close() }

func annotations(m map[string]any) *mcp.ToolAnnotations {
	if len(m) == 0 {
		return nil
	}
	return &mcp.ToolAnnotations{}
}

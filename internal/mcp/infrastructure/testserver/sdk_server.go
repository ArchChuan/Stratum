package testserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool is a declarative tool description for SDKServer fixtures. The shape is
// kept from the legacy fake_server so tests keep a stable fixture DSL, but the
// server behind it is now the official SDK.
type Tool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

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

// annotations maps the fixture DSL hints onto the SDK's typed ToolAnnotations.
// The legacy fake_server exposed these as opaque JSON; the SDK marshals each
// hint from its typed fields, so an unparsed map (as before) silently dropped
// every value and a fixture requesting destructiveHint:true advertised the
// opposite.
func annotations(m map[string]any) *mcp.ToolAnnotations {
	if len(m) == 0 {
		return nil
	}
	a := &mcp.ToolAnnotations{}
	if v, ok := m["readOnlyHint"].(bool); ok {
		a.ReadOnlyHint = v
	}
	if v, ok := m["destructiveHint"].(bool); ok {
		a.DestructiveHint = &v
	}
	if v, ok := m["idempotentHint"].(bool); ok {
		a.IdempotentHint = v
	}
	if v, ok := m["openWorldHint"].(bool); ok {
		a.OpenWorldHint = &v
	}
	if v, ok := m["title"].(string); ok {
		a.Title = v
	}
	return a
}

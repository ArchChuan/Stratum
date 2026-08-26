package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// mcpToolInvoker abstracts MCP tool invocation so tests can substitute a fake.
type mcpToolInvoker interface {
	CallTool(ctx context.Context, serverID, tool string, args map[string]any) (string, error)
}

// liveMCPInvoker connects to MCP servers declared in the point snapshot and
// calls tools over the streamable-HTTP transport (the same protocol the
// product's internal/mcp/infrastructure client speaks).
type liveMCPInvoker struct {
	mu       sync.Mutex
	sessions map[string]*mcp.ClientSession
	servers  map[string]mcpServerConfig
}

// mcpServerConfig declares one MCP server endpoint. The go-sdk
// StreamableClientTransport has no header field, so only the URL is carried.
type mcpServerConfig struct {
	URL string `yaml:"url" json:"url"`
}

func (l *liveMCPInvoker) CallTool(ctx context.Context, serverID, tool string, args map[string]any) (string, error) {
	sess, err := l.session(ctx, serverID)
	if err != nil {
		return "", err
	}
	argJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode tool args: %w", err)
	}
	result, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      tool,
		Arguments: json.RawMessage(argJSON),
	})
	if err != nil {
		return "", &infraError{fmt.Errorf("mcp tools/call %s.%s: %w", serverID, tool, err)}
	}
	return mcpResultText(result), nil
}

func (l *liveMCPInvoker) session(ctx context.Context, serverID string) (*mcp.ClientSession, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sess, ok := l.sessions[serverID]; ok {
		return sess, nil
	}
	cfg, ok := l.servers[serverID]
	if !ok {
		return nil, &infraError{fmt.Errorf("mcp server %q not declared in point", serverID)}
	}
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "stratum-eval-check", Version: "1.0"}, nil)
	sess, err := sdkClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: cfg.URL}, &mcp.ClientSessionOptions{
		ProtocolVersion: constants.MCPProtocolVersion,
	})
	if err != nil {
		return nil, &infraError{fmt.Errorf("connect mcp server %s: %w", serverID, err)}
	}
	l.sessions[serverID] = sess
	return sess, nil
}

// Close tears down every live session, releasing the SSE streams so a local
// test server can shut down cleanly. It closes all sessions and reports the
// first error encountered.
func (l *liveMCPInvoker) Close(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for id, sess := range l.sessions {
		if err := sess.Close(); err != nil && firstErr == nil {
			firstErr = &infraError{fmt.Errorf("close mcp session %s: %w", id, err)}
		}
	}
	l.sessions = map[string]*mcp.ClientSession{}
	return firstErr
}

// mcpResultText extracts the textual content of an MCP tool result.
func mcpResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var out strings.Builder
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			out.WriteString(c.Text)
		default:
			if b, err := json.Marshal(content); err == nil {
				out.Write(b)
			}
		}
	}
	return out.String()
}

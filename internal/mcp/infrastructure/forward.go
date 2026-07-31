package infrastructure

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// internalForwardTimeout caps total wall time for a forwarded tool call.
const internalForwardTimeout = 30 * time.Second

// forwardRequest is the JSON body sent to the owner node.
type forwardRequest struct {
	TenantID string         `json:"tenant_id"`
	ServerID string         `json:"server_id"`
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
}

// GetOwnerNode returns the owner node ID for a stdio server, or empty when
// the server is not stdio or has no owner row.
func (m *ClientManager) GetOwnerNode(ctx context.Context, serverID string) (string, error) {
	if m.pool == nil {
		return m.nodeID, nil
	}
	var transport, ownerNode string
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT transport, COALESCE(owner_node,'') FROM mcp_configs WHERE id=$1`,
			serverID,
		).Scan(&transport, &ownerNode)
	})
	if err != nil {
		return "", fmt.Errorf("mcp forward: get owner node: %w", err)
	}
	if transport != "stdio" {
		return "", nil
	}
	return ownerNode, nil
}

// SetForwardHTTPTransport stores the mTLS transport used for internal
// node-to-node forwarding. Pass nil to reset to a minimal TLS fallback.
func (m *ClientManager) SetForwardHTTPTransport(transport *http.Transport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fwdTransport = transport
}

// forwardHTTPTransport returns the configured forwarding transport or a
// minimal TLS fallback.
func (m *ClientManager) forwardHTTPTransport() *http.Transport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fwdTransport != nil {
		return m.fwdTransport
	}
	return &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

// ForwardToolCall sends a tool execution request to the owner node via the
// internal mTLS endpoint. Only used when this node does not host the target
// stdio server.
func (m *ClientManager) ForwardToolCall(
	ctx context.Context,
	ownerHTTPAddr string,
	tenantID string,
	serverID string,
	toolName string,
	args map[string]any,
) (any, error) {
	req := forwardRequest{
		TenantID: tenantID,
		ServerID: serverID,
		ToolName: toolName,
		Args:     args,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp forward: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, internalForwardTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(
		callCtx, http.MethodPost,
		"https://"+ownerHTTPAddr+"/internal/mcp/tools/call",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("mcp forward: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Transport: m.forwardHTTPTransport(),
		Timeout:   internalForwardTimeout,
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp forward: request to %s: %w", ownerHTTPAddr, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, constants.MCPStdioMessageMaxBytes))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp forward: %s returned %d: %s",
			ownerHTTPAddr, resp.StatusCode, string(respBody))
	}

	var fwd mcpdomain.ForwardedCallResult
	if err := json.Unmarshal(respBody, &fwd); err != nil {
		return nil, fmt.Errorf("mcp forward: decode response: %w", err)
	}
	if fwd.Error != "" {
		return nil, fmt.Errorf("mcp forward: %s", fwd.Error)
	}

	var result any
	if len(fwd.Result) > 0 {
		if err := json.Unmarshal(fwd.Result, &result); err != nil {
			return nil, fmt.Errorf("mcp forward: decode result: %w", err)
		}
	}
	return result, nil
}

// HandleForwardedToolCall executes a tool call locally on behalf of another
// node in the cluster. The caller is already authenticated via mTLS at the
// internal HTTP layer.
func (m *ClientManager) HandleForwardedToolCall(
	ctx context.Context,
	tenantID, serverID, toolName string,
	args map[string]any,
) (mcpdomain.ForwardedCallResult, error) {
	client, err := m.getOrRestoreClient(ctx, serverID)
	if err != nil {
		return mcpdomain.ForwardedCallResult{
			Error: fmt.Sprintf("server %s not connected on this node", serverID),
		}, nil
	}

	output, err := client.CallTool(ctx, toolName, args)
	if err != nil {
		return mcpdomain.ForwardedCallResult{Error: err.Error()}, nil
	}

	raw, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		return mcpdomain.ForwardedCallResult{
			Error: fmt.Sprintf("marshal tool result: %v", marshalErr),
		}, nil
	}
	return mcpdomain.ForwardedCallResult{Result: raw}, nil
}

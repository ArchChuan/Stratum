package infrastructure

import (
	"net/url"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// newSDKTransport builds the SDK streamable-HTTP transport for one MCP
// server connection. The SDK transport is single-connect: a reconnect must
// construct a fresh transport, because the session contract is one
// transport per Client.Connect. All SDK imports live in this file and
// sdk_client.go so an SDK upgrade stays a two-file change plus the
// interoperability tests.
func newSDKTransport(origin *url.URL, cfg *MCPServerConfig, policy URLPolicyOption) *mcp.StreamableClientTransport {
	return &mcp.StreamableClientTransport{
		Endpoint:   origin.String(),
		HTTPClient: newSecureHTTPClient(origin, cfg, policy),
		MaxRetries: constants.MCPMaxRetries,
		// DisableStandaloneSSE stays false (the default): standalone SSE is
		// the only channel for server→client notifications and Last-Event-ID
		// resumption under the 2025-06-18 protocol, and reconnect tests rely
		// on it.
		// OAuthHandler stays nil: OAuth2 configs are rejected at doConnect
		// (ErrUnsupportedAuth), so the SDK never needs a flow here.
	}
}

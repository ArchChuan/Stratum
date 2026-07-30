package constants

// MCPProtocolVersion is the MCP protocol version string sent by the client
// during initialize and returned by test servers. All MCP servers and clients
// in this project must use the same version for consistency.
//
// 2025-06-18 adds streamable HTTP transport as the recommended transport,
// while keeping the JSON-RPC initialize handshake model.
const MCPProtocolVersion = "2025-06-18"

package infrastructure

import (
	"context"
	"errors"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
)

// translateSDKError maps SDK errors to safe domain sentinels. The SDK decodes
// non-2xx response bodies into its error value (MCPGODEBUG=noprotocolerrorbody
// defaults to off), so the original error text can echo an Authorization
// header back at us; it must never reach logs or API responses. Only the
// error category is projected. Deployment may additionally set
// MCPGODEBUG=noprotocolerrorbody=1 as belt-and-braces (not a code change).
//
// context.Canceled is passed through as the bare sentinel (not projected to
// ErrTransportTimeout): it is the caller's own context and must not be treated
// as a transport fault (see unhealthyError).
//
// The SDK distinguishes two failure families, and they are intentionally NOT
// projected the same way:
//
//   - Transient HTTP statuses (429/502/503/504) and per-call JSON-RPC errors
//     are wrapped in its internal jsonrpc2.ErrRejected, which explicitly does
//     not break the connection (streamable.go: "Transient errors should not
//     break the connection"). jsonrpc2 is an internal package, so ErrRejected
//     cannot be imported here; everything in this family falls into
//     ErrTransportFailed and must NOT mark the client unhealthy — killing a
//     session the SDK deliberately kept alive on one application-level error
//     would rebuild it every MCPMinReconnectInterval forever.
//   - Real connection death (c.fail on a non-rejected error, session expiry)
//     surfaces as ErrConnectionClosed / ErrSessionMissing, which are handled
//     above.
//
// An oversized SSE frame (lineLimitedReader) is not distinguishable here: the
// SDK swallows the stream-read sentinel and reports the generic "request
// terminated without response" on the affected call (streamable.go handles
// scanEvents termination that way). That generic text carries no error-chain
// symbol, so it falls into ErrTransportFailed like everything else — which is
// the correct outcome anyway: an oversized frame fails that single call
// closed while the session stays usable for new streams, so it must not mark
// the client unhealthy. A truly dead session still surfaces through
// ErrSessionMissing / ErrConnectionClosed and the manager's 30s health check.
func translateSDKError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mcp.ErrSessionMissing):
		return mcpdomain.ErrSessionMissing
	case errors.Is(err, mcp.ErrConnectionClosed):
		return ErrClientClosed
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTransportTimeout
	case errors.Is(err, context.Canceled):
		return context.Canceled
	default:
		return mcpdomain.ErrTransportFailed
	}
}

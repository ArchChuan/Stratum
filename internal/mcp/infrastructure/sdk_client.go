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
// context.Canceled is passed through unchanged (not projected to
// ErrTransportTimeout): it is the caller's own context, carries no
// server-controlled text, and must not be treated as a transport fault (see
// unhealthyError). The SDK's rejection family (429/502/503/504 etc.) wraps
// its internal jsonrpc2.ErrRejected, which is not importable from outside the
// module and not re-exported, so it cannot be distinguished here; it falls
// into ErrTransportFailed. Callers may conservatively mark the client
// unhealthy on that sentinel — the manager's MCPMinReconnectInterval gate
// throttles rebuilds and a fresh session is harmless.
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
		return err
	default:
		return mcpdomain.ErrTransportFailed
	}
}

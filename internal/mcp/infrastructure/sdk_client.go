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
func translateSDKError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mcp.ErrSessionMissing):
		return mcpdomain.ErrSessionMissing
	case errors.Is(err, mcp.ErrConnectionClosed):
		return ErrClientClosed
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ErrTransportTimeout
	default:
		// Includes 429/502/503/504 rejection semantics and any other
		// transport failure; the SDK keeps the session alive for the
		// rejection family, this projection just hides the server-controlled
		// text.
		return mcpdomain.ErrTransportFailed
	}
}

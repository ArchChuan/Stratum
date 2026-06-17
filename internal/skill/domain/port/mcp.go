package port

import "context"

type MCPInvoker interface {
	Invoke(ctx context.Context, serverID, tool string, args map[string]any) (map[string]any, error)
}

package platformmcp

import "context"

type InvocationContext struct {
	TenantID    string
	UserID      string
	AgentID     string
	ExecutionID string
	ApprovalID  string
}

type invocationContextKey struct{}

func WithInvocationContext(ctx context.Context, invocation InvocationContext) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationContextFrom(ctx context.Context) (InvocationContext, bool) {
	invocation, ok := ctx.Value(invocationContextKey{}).(InvocationContext)
	return invocation, ok
}

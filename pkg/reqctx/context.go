package reqctx

import "context"

type traceIDKey struct{}
type tenantIDKey struct{}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, id)
}

func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey{}).(string)
	return v
}

func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tenantIDKey{}, id)
}

func TenantIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(tenantIDKey{}).(string)
	return v
}

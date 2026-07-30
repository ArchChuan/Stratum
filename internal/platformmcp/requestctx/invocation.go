package requestctx

import "context"

type invocationTokenKey struct{}

func WithInvocationToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, invocationTokenKey{}, token)
}

func InvocationToken(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(invocationTokenKey{}).(string)
	return token, ok && token != ""
}

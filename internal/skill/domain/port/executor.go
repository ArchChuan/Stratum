package port

import "context"

type Executor interface {
	Execute(ctx context.Context, input map[string]any) (map[string]any, error)
}

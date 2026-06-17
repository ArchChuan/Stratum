package port

import "context"

type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	Embed(ctx context.Context, text string) ([]float32, error)
	Health(ctx context.Context) error
}

type CompletionRequest struct {
	Model       string
	Messages    []Message
	Temperature float32
	MaxTokens   int
}

type CompletionResponse struct {
	Content string
	Tokens  int
}

type Message struct {
	Role, Content string
}

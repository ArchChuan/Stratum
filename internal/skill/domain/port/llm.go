package port

import "context"

type LLMCompleter interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
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

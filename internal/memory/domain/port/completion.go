package port

import "context"

type CompletionMessage struct {
	Role    string
	Content string
}

type CompletionRequest struct {
	Model       string
	Messages    []CompletionMessage
	Temperature float64
	MaxTokens   int
}

type CompletionResponse struct {
	Content          string
	CompletionTokens int
}

type Completer interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}

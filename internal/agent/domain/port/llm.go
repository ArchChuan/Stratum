package port

import "context"

type LLMCompleter interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

type CompletionRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	Temperature float32
	MaxTokens   int
}

type CompletionResponse struct {
	Content   string
	ToolCalls []ToolCall
	Tokens    int
}

type Message struct {
	Role, Content string
}

type ToolDef struct {
	Name, Description string
	Schema            map[string]any
}

type ToolCall struct {
	Name string
	Args map[string]any
}

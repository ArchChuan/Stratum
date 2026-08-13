package port

import "context"

type CompletionMessage struct {
	Role    string
	Content string
}

// ResponseFormat 请求 provider 强制结构化输出（{"type":"json_object"}）。
// memport 不能 import llmgateway domain（DDD：domain 仅依赖 stdlib + constants），
// 因此这里是 llmgateway.ResponseFormat 的本地对等类型，由 wiring 适配透传。
type ResponseFormat struct {
	Type string
}

type CompletionRequest struct {
	Model          string
	Messages       []CompletionMessage
	Temperature    float64
	MaxTokens      int
	ResponseFormat *ResponseFormat
}

type CompletionResponse struct {
	Content          string
	CompletionTokens int
}

type Completer interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}

package domain

import "context"

// LLM IO types — 由 infra/gateway 实现，被各 bounded context 通过本地 port 接口消费。
// 放 domain 层是为了消除 "外部消费者 import llmgateway/infrastructure" 的越层依赖。
// 仅含 stdlib 依赖，符合 domain 零第三方约束。

// LLMCompleter 是消费侧调用 LLM 的最小接口。
// *llmgateway/infrastructure.Gateway 结构性满足该接口。
type LLMCompleter interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
}

type completerCtxKey struct{}

// WithCompleter 把 LLMCompleter 注入 ctx 供下游覆盖默认实现。
func WithCompleter(ctx context.Context, c LLMCompleter) context.Context {
	return context.WithValue(ctx, completerCtxKey{}, c)
}

// CompleterFromContext 从 ctx 取出 LLMCompleter；不存在返回 (nil, false)。
func CompleterFromContext(ctx context.Context) (LLMCompleter, bool) {
	c, ok := ctx.Value(completerCtxKey{}).(LLMCompleter)
	return c, ok
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	// ReasoningEffort 是采样强度档位:""|low|medium|high。OpenAI 兼容协议
	// 直传 body;Anthropic 由 provider 映射 extended_thinking。网关按候选
	// 模型推理能力门控,未知/非推理模型清空,禁止盲透传(严格端点 400 是
	// 永久错误,会中止 fallback 链)。
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
	MaxTokens       int     `json:"max_tokens,omitempty"`
	TopP            float32 `json:"top_p,omitempty"`
	Tools           []Tool  `json:"tools,omitempty"`
	ToolChoice      string  `json:"tool_choice,omitempty"`
	Stream          bool    `json:"stream,omitempty"`
	// NoPrimaryRetry 禁止 gateway 对主模型瞬态失败的立即重试（0 值 = 默认
	// 允许一次立即重试）。压缩路径用：时间片内主模型一次尝试失败直接降级候选。
	// json:"-"：路由选项，禁止随请求体泄漏给 provider。
	NoPrimaryRetry bool `json:"-"`
	// MaxCandidates 限制 fallback 候选数量；0 = gateway 默认上限。
	MaxCandidates int `json:"-"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type CompletionResponse struct {
	Content   string     `json:"content"`
	Model     string     `json:"model"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     TokenUsage `json:"usage"`
	// ModelResolved 是本次请求实际成功调用的模型名。未降级时与请求 Model
	// 相同，降级后为 fallback 候选名。
	ModelResolved string `json:"model_resolved,omitempty"`
	// ModelRoutedVia 是本次请求实际尝试过的模型链（主模型在前，仅含失败
	// 尝试与最终成功者）。单次成功时为仅含主模型。
	ModelRoutedVia []string `json:"model_routed_via,omitempty"`
}

type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type EmbeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Package llmgateway provides LLM gateway abstraction.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

const (
	llmStatusSuccess = "success"
	llmStatusError   = "error"
)

// LLM IO 类型在 domain 层定义；infra 通过 alias 暴露给内部实现，
// 同时允许跨 ctx 消费者直接 import domain，避免越层依赖。
type (
	Tool               = domain.Tool
	ToolFunction       = domain.ToolFunction
	ToolCall           = domain.ToolCall
	Message            = domain.Message
	CompletionRequest  = domain.CompletionRequest
	TokenUsage         = domain.TokenUsage
	CompletionResponse = domain.CompletionResponse
	EmbeddingRequest   = domain.EmbeddingRequest
	EmbeddingResponse  = domain.EmbeddingResponse
)

// openAICompletionResp is the shared decode type for OpenAI-compatible completion responses.
type openAICompletionResp struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Model string     `json:"model"`
	Usage TokenUsage `json:"usage"`
}

// openAIStreamChunk is the shared decode type for OpenAI-compatible SSE stream chunks.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string      `json:"model"`
	Usage *TokenUsage `json:"usage"`
}

// streamToolCallDelta is the per-chunk tool call fragment from an SSE stream.
// Index identifies which tool call slot this delta belongs to.
type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIEmbedResp is the shared decode type for OpenAI-compatible embedding responses.
type openAIEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Gateway delegates LLM requests to the ModelRegistry, resolving each model
// name to a provider configuration and protocol at call time.
type Gateway struct {
	registry    *ModelRegistry
	chatProtos  map[domain.ProviderKind]ChatProtocol
	embedProtos map[domain.ProviderKind]EmbedProtocol
	metrics     observability.MetricsProvider
	logger      *zap.Logger
}

// NewGateway creates a Gateway with a no-op metrics provider.
// Call WithMetrics to inject a real provider.
func NewGateway(registry *ModelRegistry, chatProtos map[domain.ProviderKind]ChatProtocol, embedProtos map[domain.ProviderKind]EmbedProtocol) *Gateway {
	return &Gateway{
		registry:    registry,
		chatProtos:  chatProtos,
		embedProtos: embedProtos,
		metrics:     observability.NoopMetrics{},
		logger:      zap.NewNop(),
	}
}

// WithMetrics injects a MetricsProvider into the gateway.
func (g *Gateway) WithMetrics(m observability.MetricsProvider) *Gateway {
	g.metrics = m
	return g
}

// WithLogger injects a logger into the gateway.
func (g *Gateway) WithLogger(l *zap.Logger) *Gateway {
	g.logger = l
	return g
}

// chainLink 是 fallback 链中的一个模型：主模型 + 已解析候选。
type chainLink struct {
	Model    string
	Config   ProviderConfig
	Protocol ChatProtocol
}

// routedInfo 记录一次请求实际尝试过的模型链与最终成功模型。
type routedInfo struct {
	all  []string // 尝试过的模型（含失败与最终成功者），主模型在前
	last string   // 最终成功模型
}

// Complete resolves the model via the registry and delegates to the resolved
// ChatProtocol. 瞬态失败沿 fallback 链降级；主模型失败立即重试 1 次。
func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	links, err := g.resolveChain(ctx, tenantID, req.Model)
	if err != nil {
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("llmgateway: resolve model %q: %w", req.Model, err)
	}
	g.logLLMRequest(ctx, req, links[0].Config, false)
	resp, routed, err := g.invokeWithFallback(ctx, req, links, false, nil)
	if err != nil {
		return nil, err
	}
	resp.ModelResolved = routed.last
	resp.ModelRoutedVia = routed.all
	return resp, nil
}

// CompleteStream resolves the model via the registry and delegates to the
// resolved ChatProtocol's streaming method. 仅「首 token 发出前」的失败可
// 降级；首 token 已流出后的中途失败不得降级（避免向客户端重复输出），
// 包装为 permanent 错误传播。
func (g *Gateway) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	links, err := g.resolveChain(ctx, tenantID, req.Model)
	if err != nil {
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("llmgateway: resolve model %q: %w", req.Model, err)
	}
	g.logLLMRequest(ctx, req, links[0].Config, true)
	resp, routed, err := g.invokeWithFallback(ctx, req, links, true, onToken)
	if err != nil {
		return nil, err
	}
	resp.ModelResolved = routed.last
	resp.ModelRoutedVia = routed.all
	return resp, nil
}

// resolveChain 组装 fallback 链：主模型 + 有序列举的候选（上限
// constants.MaxModelFallbackCandidates）。主模型必须可解析，否则直接报错。
func (g *Gateway) resolveChain(ctx context.Context, tenantID, model string) ([]chainLink, error) {
	cfg, proto, err := g.registry.Resolve(ctx, tenantID, model)
	if err != nil {
		return nil, err
	}
	links := []chainLink{{Model: model, Config: cfg, Protocol: proto}}
	cands, err := g.registry.ResolveFallbackCandidates(ctx, tenantID, model)
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		links = append(links, chainLink(c))
	}
	return links, nil
}

// invokeWithFallback 沿链编排调用：主模型（i==0）瞬态失败立即重试 1 次
// （req.NoPrimaryRetry=true 时跳过），仍失败或候选失败时瞬态错误降级到
// 下一候选；永久错误、context.Canceled 或流式已出首 token 立即停止。候选
// 链按 req.MaxCandidates 截断（0 = 默认上限）。耗尽返回包装全部尝试的
// permanent 错误，绝不静默成功。每次降级调用 IncRouteFallback(from, to)。
func (g *Gateway) invokeWithFallback(
	ctx context.Context,
	req *CompletionRequest,
	links []chainLink,
	stream bool,
	onToken func(string),
) (*CompletionResponse, routedInfo, error) {
	// 候选链按 req.MaxCandidates 截断：links[0] 是主模型，其余为候选。
	// resolveChain 已按 constants.MaxModelFallbackCandidates 封顶，此处只缩不放。
	if req.MaxCandidates > 0 && len(links) > 1+req.MaxCandidates {
		links = links[:1+req.MaxCandidates]
	}
	tried := make([]string, 0, len(links))
	attempts := make([]error, 0, len(links)+1)
	for i, link := range links {
		if i > 0 {
			g.metrics.IncRouteFallback(links[i-1].Model, link.Model)
		}
		resp, err, stop := g.invokeCandidate(ctx, req, link, stream, onToken, i == 0)
		if stop {
			return nil, routedInfo{all: tried}, err
		}
		if resp != nil {
			return resp, routedInfo{all: appendModel(tried, link.Model), last: link.Model}, nil
		}
		tried = appendModel(tried, link.Model)
		attempts = append(attempts, fmt.Errorf("%s: %w", link.Model, err))
	}
	return nil, routedInfo{all: tried}, markPermanent(fmt.Errorf(
		"llmgateway: fallback chain exhausted (%d attempts): %w", len(attempts), errors.Join(attempts...)))
}

// invokeCandidate 完成单个链环节点的所有尝试：主模型（isPrimary）瞬态失败
// 立即重试 1 次（req.NoPrimaryRetry=true 时跳过），候选不重试。返回：
//   - resp != nil：成功；
//   - stop=true：链必须终止（永久错误 / 流式已出首 token），err 直接传播；
//   - 其余：瞬态失败，链继续降级到下一候选。
func (g *Gateway) invokeCandidate(
	ctx context.Context,
	req *CompletionRequest,
	link chainLink,
	stream bool,
	onToken func(string),
	isPrimary bool,
) (*CompletionResponse, error, bool) {
	resp, outputStarted, err := g.invoke(ctx, req, link, stream, onToken)
	if err == nil {
		return resp, nil, false
	}
	if isPrimary && !req.NoPrimaryRetry && !outputStarted && isTransient(err) {
		// 主模型 1 次立即重试，仍失败才进入降级。
		resp, outputStarted, err = g.invoke(ctx, req, link, stream, onToken)
		if err == nil {
			return resp, nil, false
		}
	}
	if outputStarted {
		// 流式已向客户端输出首 token，重试或降级都会产生重复输出。
		return nil, markPermanent(fmt.Errorf(
			"llmgateway: stream failed after first token on model %q: %w", link.Model, err)), true
	}
	if !isTransient(err) {
		return nil, markPermanent(err), true
	}
	return nil, err, false
}

// appendModel 向模型链追加 model，连续重复（主模型立即重试）不重复记录。
func appendModel(tried []string, model string) []string {
	if len(tried) > 0 && tried[len(tried)-1] == model {
		return tried
	}
	return append(tried, model)
}

// invoke 分发单次尝试：非流式走 Complete，流式走 CompleteStream。
// fallback 候选尝试必须携带候选模型名：协议层请求体按 req.Model 构造，
// 否则候选 provider 会收到主模型名而报错。
func (g *Gateway) invoke(
	ctx context.Context,
	req *CompletionRequest,
	link chainLink,
	stream bool,
	onToken func(string),
) (*CompletionResponse, bool, error) {
	attemptReq := req
	if link.Model != req.Model {
		cloned := *req
		cloned.Model = link.Model
		attemptReq = &cloned
	}
	if stream {
		return g.invokeStream(ctx, attemptReq, link, onToken)
	}
	resp, err := g.invokeComplete(ctx, attemptReq, link)
	return resp, false, err
}

// invokeComplete 是主模型或候选的单次非流式调用：协议调用 + 指标 + 日志。
func (g *Gateway) invokeComplete(ctx context.Context, req *CompletionRequest, link chainLink) (*CompletionResponse, error) {
	start := time.Now()
	resp, err := link.Protocol.Complete(ctx, link.Config, req)
	elapsed := time.Since(start).Seconds()

	status := llmStatusSuccess
	if err != nil {
		status = llmStatusError
	}
	g.metrics.IncLLMRequest(link.Model, link.Config.Name, status)
	g.metrics.RecordLLMRequestDuration(link.Model, link.Config.Name, elapsed)

	if err == nil && resp != nil {
		if resp.Usage.PromptTokens > 0 {
			g.metrics.IncLLMTokenUsage(link.Model, "prompt", int64(resp.Usage.PromptTokens))
			g.metrics.RecordLLMTokenHistogram(link.Model, "prompt", float64(resp.Usage.PromptTokens))
		}
		if resp.Usage.CompletionTokens > 0 {
			g.metrics.IncLLMTokenUsage(link.Model, "completion", int64(resp.Usage.CompletionTokens))
			g.metrics.RecordLLMTokenHistogram(link.Model, "completion", float64(resp.Usage.CompletionTokens))
		}
	}
	g.logComplete(ctx, link, resp, err, elapsed, false)
	return resp, err
}

// invokeStream 是单次流式尝试：otel span + TTFT + 首 token 标记。
// outputStarted 表示至少一个 token 已通过 onToken 流出（fallback 判定用）。
func (g *Gateway) invokeStream(
	ctx context.Context,
	req *CompletionRequest,
	link chainLink,
	onToken func(string),
) (*CompletionResponse, bool, error) {
	start := time.Now()
	tracer := otel.Tracer("stratum/llmgateway")
	_, llmGWSpan := tracer.Start(ctx, "llm.complete",
		oteltrace.WithAttributes(
			attribute.String("llm.model", link.Model),
			attribute.String("llm.provider", link.Config.Name),
			attribute.Bool("llm.stream", true),
		),
	)
	defer llmGWSpan.End()

	var (
		resp          *CompletionResponse
		ttftRecorded  bool
		outputStarted bool
	)
	wrappedOnToken := func(t string) {
		if !ttftRecorded {
			ttftRecorded = true
			g.metrics.RecordLLMFirstTokenLatency(link.Model, link.Config.Name, time.Since(start).Seconds())
		}
		outputStarted = true
		onToken(t)
	}

	resp, err := link.Protocol.CompleteStream(ctx, link.Config, req, wrappedOnToken)
	elapsed := time.Since(start).Seconds()

	status := llmStatusSuccess
	if err != nil {
		status = llmStatusError
	}
	g.metrics.IncLLMRequest(link.Model, link.Config.Name, status)
	g.metrics.RecordLLMRequestDuration(link.Model, link.Config.Name, elapsed)

	if err == nil && resp != nil {
		llmGWSpan.SetAttributes(
			attribute.Int("llm.prompt_tokens", resp.Usage.PromptTokens),
			attribute.Int("llm.completion_tokens", resp.Usage.CompletionTokens),
		)
		if resp.Usage.PromptTokens > 0 {
			g.metrics.IncLLMTokenUsage(link.Model, "prompt", int64(resp.Usage.PromptTokens))
		}
		if resp.Usage.CompletionTokens > 0 {
			g.metrics.IncLLMTokenUsage(link.Model, "completion", int64(resp.Usage.CompletionTokens))
		}
	} else if err != nil {
		llmGWSpan.RecordError(err)
		llmGWSpan.SetStatus(codes.Error, "llm provider call failed")
	}
	g.logComplete(ctx, link, resp, err, elapsed, true)
	return resp, outputStarted, err
}

// logLLMRequest 记录链入口请求日志（每个逻辑请求一次）。
func (g *Gateway) logLLMRequest(ctx context.Context, req *CompletionRequest, cfg ProviderConfig, stream bool) {
	fields := []zap.Field{
		zap.String("trace_id", reqctx.TraceIDFromContext(ctx)),
		zap.String("tenant_id", reqctx.TenantIDFromContext(ctx)),
		zap.String("model", req.Model),
		zap.String("provider", cfg.Name),
		zap.Int("tool_count", len(req.Tools)),
	}
	if stream {
		fields = append(fields, zap.Bool("stream", true))
	}
	if req.ToolChoice != "" {
		fields = append(fields, zap.String("tool_choice", req.ToolChoice))
	}
	g.logger.Info("llm.request", fields...)
}

// logComplete 记录单次尝试的完成日志（成功 Info，失败 Error）。
func (g *Gateway) logComplete(
	ctx context.Context,
	link chainLink,
	resp *CompletionResponse,
	err error,
	elapsed float64,
	stream ...bool,
) {
	fields := []zap.Field{
		zap.String("trace_id", reqctx.TraceIDFromContext(ctx)),
		zap.String("tenant_id", reqctx.TenantIDFromContext(ctx)),
		zap.String("model", link.Model),
		zap.String("provider", link.Config.Name),
		zap.Bool("stream", len(stream) > 0 && stream[0]),
		zap.Int64("latency_ms", int64(elapsed*1000)),
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		g.logger.Error("llm.complete", fields...)
		return
	}
	if resp == nil {
		return
	}
	fields = append(fields,
		zap.Int("prompt_tokens", resp.Usage.PromptTokens),
		zap.Int("completion_tokens", resp.Usage.CompletionTokens),
	)
	g.logger.Info("llm.complete", fields...)
}

// CreateEmbeddings resolves the embedding model via the registry and delegates
// to the resolved EmbedProtocol.
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	cfg, proto, err := g.registry.ResolveEmbedding(ctx, tenantID, req.Model)
	if err != nil {
		return nil, fmt.Errorf("llmgateway: resolve embedding model %q: %w", req.Model, err)
	}
	return proto.CreateEmbeddings(ctx, cfg, req)
}

// Health returns nil. Per-tenant health checks are delegated to the
// ModelRegistry and are not performed at the global Gateway level.
func (g *Gateway) Health(context.Context) error {
	return nil
}

// ListEmbeddingModels returns an empty slice. Tenant-scoped model lists are
// available via ListEmbeddingModelsByTenant.
func (g *Gateway) ListEmbeddingModels() []string {
	return []string{}
}

// ListChatModels returns an empty slice. Tenant-scoped model lists are
// available via ListChatModelsByTenant.
func (g *Gateway) ListChatModels() []string {
	return []string{}
}

// ListChatModelsByTenant returns sorted enabled chat model names for the
// given tenant, delegating to the registry.
func (g *Gateway) ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListChatModelsByTenant(ctx, tenantID)
}

// ListEmbeddingModelsByTenant returns sorted enabled embedding model names
// for the given tenant, delegating to the registry.
func (g *Gateway) ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListEmbeddingModelsByTenant(ctx, tenantID)
}

// WithGateway returns a new context carrying gw as the LLM gateway override.
// 内部委派给 domain.WithCompleter，使消费方可仅依赖 domain 接口。
func WithGateway(ctx context.Context, gw *Gateway) context.Context {
	return domain.WithCompleter(ctx, gw)
}

// GatewayFromContext returns the gateway stored in ctx (from WithGateway), or (nil, false).
func GatewayFromContext(ctx context.Context) (*Gateway, bool) {
	c, ok := domain.CompleterFromContext(ctx)
	if !ok {
		return nil, false
	}
	gw, ok := c.(*Gateway)
	return gw, ok
}

package application

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// TokenLedger 聚合 agent 侧的 token 估算、记录、成本计算。
type TokenLedger struct {
	metrics observability.MetricsProvider
	tracer  trace.Tracer
	logger  *zap.Logger
}

func NewTokenLedger(metrics observability.MetricsProvider, tracer trace.Tracer, logger *zap.Logger) *TokenLedger {
	return &TokenLedger{metrics: metrics, tracer: tracer, logger: logger}
}

// UsageSummary 封装单次 LLM 调用的 token + 成本。
type UsageSummary struct {
	Prompt     int
	Completion int
	Total      int
	CostUSD    float64
}

// Record 在每次 LLM 调用返回后调用，完成成本计算、Prometheus 打点、OTEL span、zap 日志。
// 返回 (total tokens, cost USD)。
func (l *TokenLedger) Record(ctx context.Context, model string, usage port.TokenUsage) (int, float64) {
	cost := tokenutil.CostUSD(usage.Prompt, usage.Completion, model)

	if l.metrics != nil {
		if usage.Prompt > 0 {
			l.metrics.IncLLMTokenUsage(model, "prompt", int64(usage.Prompt))
			l.metrics.RecordLLMTokenHistogram(model, "prompt", float64(usage.Prompt))
		}
		if usage.Completion > 0 {
			l.metrics.IncLLMTokenUsage(model, "completion", int64(usage.Completion))
			l.metrics.RecordLLMTokenHistogram(model, "completion", float64(usage.Completion))
		}
	}

	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(
			attribute.Int("llm.prompt_tokens", usage.Prompt),
			attribute.Int("llm.completion_tokens", usage.Completion),
			attribute.Float64("llm.cost_usd", cost),
		)
	}

	if l.logger != nil {
		l.logger.Debug("token.record",
			zap.String("model", model),
			zap.Int("prompt_tokens", usage.Prompt),
			zap.Int("completion_tokens", usage.Completion),
			zap.Int("total_tokens", usage.Total),
			zap.Float64("cost_usd", cost),
		)
	}

	return usage.Total, cost
}

// Estimate 估算消息列表 token 数，统一使用 tokenutil 算法。
func (l *TokenLedger) Estimate(msgs []port.LLMMessage) int {
	total := 0
	for _, m := range msgs {
		total += tokenutil.EstimateText(m.Role) + tokenutil.EstimateText(m.Content) + 4
	}
	return total
}

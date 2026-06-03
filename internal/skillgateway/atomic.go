// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type traceIDKeyType struct{}

var traceIDKey = traceIDKeyType{}

// TraceIDFromContext 从 context 提取 trace_id
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

type atomicEngine struct {
	registry *ProviderRegistry
	cb       *CircuitBreakerManager
	metrics  *observability.PrometheusMetrics
	auditor  *auditor
	logger   *zap.Logger
}

func newAtomicEngine(
	registry *ProviderRegistry,
	cb *CircuitBreakerManager,
	metrics *observability.PrometheusMetrics,
	auditor *auditor,
	logger *zap.Logger,
) *atomicEngine {
	return &atomicEngine{
		registry: registry,
		cb:       cb,
		metrics:  metrics,
		auditor:  auditor,
		logger:   logger.Named("atomic_engine"),
	}
}

const (
	defaultTimeout   = 30 * time.Second
	maxRetryDelay    = 10 * time.Second
	maxRetryAttempts = 10
)

func (e *atomicEngine) execute(ctx context.Context, req SkillRequest) (SkillResponse, error) {
	// 1. trace_id 注入/传播
	traceID := req.TraceID
	if traceID == "" {
		traceID = uuid.New().String()
	}
	ctx = context.WithValue(ctx, traceIDKey, traceID)

	// 2. 查找 provider
	provider, ok := e.registry.Resolve(req.SkillID)
	if !ok {
		return SkillResponse{}, &SkillError{
			Code:    ErrSkillNotFound,
			Message: "skill not found: " + req.SkillID,
			TraceID: traceID,
		}
	}
	skillType := provider.SkillType()

	// 3. 熔断检查
	if !e.cb.Allow(req.SkillID) {
		return SkillResponse{}, &SkillError{
			Code:    ErrCircuitOpen,
			Message: "circuit breaker open for skill: " + req.SkillID,
			TraceID: traceID,
		}
	}

	// 4. 超时配置
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// 5. 重试配置
	maxAttempts := req.Retry.MaxAttempts + 1
	if maxAttempts > maxRetryAttempts+1 {
		maxAttempts = maxRetryAttempts + 1
	}
	baseDelay := req.Retry.BaseDelay
	if baseDelay <= 0 {
		baseDelay = 100 * time.Millisecond
	}

	start := time.Now()
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 检查上游 context 是否已取消
		if err := ctx.Err(); err != nil {
			e.cb.RecordFailure(req.SkillID)
			return SkillResponse{}, &SkillError{
				Code:    ErrSkillExecFailed,
				Message: "context cancelled",
				TraceID: traceID,
				Cause:   err,
			}
		}

		// 指数退避等待（第一次不等待）
		if attempt > 0 {
			delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt-1)))
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				e.cb.RecordFailure(req.SkillID)
				return SkillResponse{}, &SkillError{
					Code:    ErrSkillExecFailed,
					Message: "context cancelled during retry backoff",
					TraceID: traceID,
					Cause:   ctx.Err(),
				}
			}
		}

		execCtx, cancel := context.WithTimeout(ctx, timeout)
		output, err := provider.Execute(execCtx, req.SkillID, req.Input)
		cancel()

		if err == nil {
			duration := time.Since(start)
			e.cb.RecordSuccess(req.SkillID)
			e.reportMetrics(req.SkillID, skillType, "success", duration)
			caller := req.Metadata["caller"]
			e.auditor.log(traceID, req.SkillID, caller, "success", duration)
			return SkillResponse{
				TraceID:  traceID,
				SkillID:  req.SkillID,
				Output:   output,
				Duration: duration,
				Metadata: req.Metadata,
			}, nil
		}

		// 超时错误不重试
		if errors.Is(err, context.DeadlineExceeded) {
			e.cb.RecordFailure(req.SkillID)
			duration := time.Since(start)
			e.reportMetrics(req.SkillID, skillType, "timeout", duration)
			e.auditor.log(traceID, req.SkillID, req.Metadata["caller"], "timeout", duration)
			return SkillResponse{}, &SkillError{
				Code:    ErrSkillTimeout,
				Message: "skill execution timeout: " + req.SkillID,
				TraceID: traceID,
				Cause:   err,
			}
		}

		lastErr = err
	}

	// 所有重试耗尽
	e.cb.RecordFailure(req.SkillID)
	duration := time.Since(start)
	e.reportMetrics(req.SkillID, skillType, "failed", duration)
	e.auditor.log(traceID, req.SkillID, req.Metadata["caller"], "failed", duration)
	return SkillResponse{}, &SkillError{
		Code:    ErrSkillExecFailed,
		Message: "skill execution failed: " + req.SkillID,
		TraceID: traceID,
		Cause:   lastErr,
	}
}

func (e *atomicEngine) reportMetrics(skillID, skillType, status string, duration time.Duration) {
	if e.metrics == nil {
		return
	}
	e.metrics.IncSkillExecution(skillID, skillType, status)
	e.metrics.RecordSkillExecutionDuration(skillID, duration.Seconds())
}

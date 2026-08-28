package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// ObservationProcessor 消费 worker 委托的落地服务接口（便于测试注入）。
type ObservationProcessor interface {
	Process(ctx context.Context, evt domain.ObservationReferenceEvent) error
}

// consumerStopGuard 幂等停止 + 循环完成信号（与 memory 的 consumerStopGuard 语义一致）。
type consumerStopGuard struct {
	stopCh chan struct{}
	doneCh chan struct{}
}

func newConsumerStopGuard() *consumerStopGuard {
	return &consumerStopGuard{stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (g *consumerStopGuard) Stop() {
	select {
	case <-g.stopCh:
	default:
		close(g.stopCh)
	}
}

// ObservationConsumerWorker 消费 evaluation.observe.{tenant} 引用事件，
// 采样后交由 ObservationService 落库。失败重投，超 MaxDeliver 进 DLQ。
// 消费循环与 memory 的 ExtractionConsumerWorker 同构（Fetch(1)+退避+stop guard）。
type ObservationConsumerWorker struct {
	consumer   jetstream.Consumer // wiring 经 pipeline.CreateConsumer 创建后注入
	js         dlqPublisher       // 死信发布（wiring 传 jetstream.JetStream）
	processor  ObservationProcessor
	metrics    observability.MetricsProvider
	logger     *zap.Logger
	ackWait    time.Duration // 心跳间隔 = ackWait/2
	maxDeliver int
	guard      *consumerStopGuard
}

func NewObservationConsumerWorker(consumer jetstream.Consumer, js dlqPublisher, processor ObservationProcessor,
	metrics observability.MetricsProvider, logger *zap.Logger, ackWait time.Duration, maxDeliver int,
) *ObservationConsumerWorker {
	return &ObservationConsumerWorker{
		consumer: consumer, js: js, processor: processor, metrics: metrics, logger: logger,
		ackWait: ackWait, maxDeliver: maxDeliver, guard: newConsumerStopGuard(),
	}
}

// Start 启动消费循环与积压指标采集（背靠背运行，Stop 统一收敛）。调用方以 go 启动。
func (w *ObservationConsumerWorker) Start(ctx context.Context) error {
	if w.consumer == nil {
		return fmt.Errorf("observation consumer: jetstream unavailable")
	}
	go w.runConsumerLoop(ctx)
	go w.reportBacklog(ctx)
	return nil
}

// Stop 停止消费循环（幂等；等待当前循环退出）。
func (w *ObservationConsumerWorker) Stop() {
	w.guard.Stop()
	<-w.guard.doneCh
}

func (w *ObservationConsumerWorker) runConsumerLoop(ctx context.Context) {
	defer close(w.guard.doneCh)
	backoff := constants.ObservationFetchBackoffBase
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.guard.stopCh:
			return
		default:
		}
		msgs, err := w.consumer.Fetch(1, jetstream.FetchMaxWait(constants.ObservationFetchMaxWait))
		if err != nil {
			w.logger.Warn("observation consumer fetch failed",
				zap.Error(err), zap.Duration("backoff", backoff))
			if !sleepObservationCtx(ctx, w.guard.stopCh, backoff) {
				return
			}
			backoff = minDuration(backoff*2, constants.ObservationFetchBackoffMax)
			continue
		}
		backoff = constants.ObservationFetchBackoffBase
		for msg := range msgs.Messages() {
			w.safeProcessMessage(ctx, msg)
		}
	}
}

// sleepObservationCtx 可中断退避；返回 false 表示应退出循环。
func sleepObservationCtx(ctx context.Context, stopCh chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	case <-t.C:
		return true
	}
}

// safeProcessMessage 捕获处理 panic（与 memory 一致：panic 即 Nak 重投）。
func (w *ObservationConsumerWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Warn("observation consumer panic", zap.Any("panic", r))
			_ = msg.Nak()
		}
	}()
	w.processMessage(ctx, msg)
}

// processMessage 反序列化 → 委托落地服务；无返回值（ack/nak/DLQ 在内部处理，memory 风格）。
func (w *ObservationConsumerWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
	defer stopHeartbeat()

	var evt domain.ObservationReferenceEvent
	if err := json.Unmarshal(msg.Data(), &evt); err != nil {
		w.metrics.IncEvalJudgeFailure("malformed_event")
		w.logger.Warn("observation malformed event",
			zap.Error(err), zap.String("subject", msg.Subject()))
		details := deadLetterDetails{
			Stage: "observation", TenantID: tenantFromObservationSubject(msg.Subject()),
			MessageID: msg.Subject(), ErrorCode: "malformed_event",
		}
		_ = deadLetterWithHeartbeat(ctx, w.js, msg, stopHeartbeat, details)
		return
	}
	// Process 契约：仅证据查询失败返回 error（= 重投）；judge 关闭 / judge 故障 /
	// 校验非法 / 落库失败均在服务内丢弃并返回 nil，不会走到这里。
	if err := w.processor.Process(ctx, evt); err != nil {
		w.logger.Warn("observation process failed",
			zap.Error(err), zap.String("subject", msg.Subject()))
		details := deadLetterDetails{
			Stage: "observation", TenantID: evt.TenantID, MessageID: evt.TraceID,
			TraceID: evt.TraceID, ErrorCode: "processor_error",
		}
		if wentDLQ, err := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, details); err != nil {
			w.logger.Warn("observation redeliver/dead-letter failed", zap.Error(err))
		} else if !wentDLQ {
			w.metrics.IncEvalJudgeFailure("redeliver")
		}
		return
	}
	stopHeartbeat()
	_ = msg.Ack()
}

// reportBacklog 周期上报 NATS 消费积压（规格 §11.2 eval_queue_backlog）。
func (w *ObservationConsumerWorker) reportBacklog(ctx context.Context) {
	ticker := time.NewTicker(constants.ObservationBacklogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.guard.stopCh:
			return
		case <-ticker.C:
			info, err := w.consumer.Info(ctx)
			if err != nil {
				w.logger.Warn("observation backlog query failed", zap.Error(err))
				continue
			}
			w.metrics.SetEvalQueueBacklog("observation", int64(info.NumPending)) //nolint:gosec // G115: 积压数受流容量约束，恒在 int64 范围内
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

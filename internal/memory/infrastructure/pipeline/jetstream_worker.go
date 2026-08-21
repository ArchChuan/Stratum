package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// runConsumerLoop 是 JetStream worker 的公共消费循环：Fetch(1) + 退避 +
// 逐条处理。process 内部负责 ack/nak/dead-letter。
func runConsumerLoop(
	ctx context.Context,
	stopCh chan struct{},
	logger *zap.Logger,
	label string,
	consumer jetstream.Consumer,
	process func(context.Context, jetstream.Msg),
) {
	backoff := constants.MemoryFetchBackoffBase
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		default:
		}

		msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			var retry bool
			backoff, retry = consumerFetchBackoff(ctx, stopCh, logger, label, backoff, err)
			if !retry {
				return
			}
			continue
		}
		backoff = constants.MemoryFetchBackoffBase
		for msg := range msgs.Messages() {
			process(ctx, msg)
		}
	}
}

// consumerFetchBackoff 处理单次 Fetch 失败：日志、退避 sleep 与指数退避
// 上限。返回 (下一退避值, 是否继续重试)；ctx/stopCh 触发时不再重试。
func consumerFetchBackoff(
	ctx context.Context,
	stopCh chan struct{},
	logger *zap.Logger,
	label string,
	backoff time.Duration,
	err error,
) (time.Duration, bool) {
	if ctx.Err() != nil {
		return backoff, false
	}
	logger.Warn("memory."+label+".fetch_failed",
		zap.Error(err),
		zap.Duration("backoff", backoff))
	if !sleepCtx(ctx, stopCh, backoff) {
		return backoff, false
	}
	if backoff < constants.MemoryFetchBackoffMax {
		backoff *= 2
		if backoff > constants.MemoryFetchBackoffMax {
			backoff = constants.MemoryFetchBackoffMax
		}
	}
	return backoff, true
}

// consumerStopGuard 封装 stopOnce 语义，供消费型 worker 复用。
type consumerStopGuard struct {
	stopCh   chan struct{}
	stopOnce sync.Once
}

func newConsumerStopGuard() consumerStopGuard {
	return consumerStopGuard{stopCh: make(chan struct{})}
}

func (g *consumerStopGuard) Stop() {
	g.stopOnce.Do(func() { close(g.stopCh) })
}

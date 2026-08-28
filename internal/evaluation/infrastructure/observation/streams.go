package observation

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EnsureStreams 幂等创建观察引用事件流与死信流（CreateOrUpdateStream，重复调用安全）。
// 观察流用 WorkQueuePolicy（单消费者语义，配合 durable consumer）；
// DLQ 流用 LimitsPolicy 保留失败消息供复盘。
func EnsureStreams(ctx context.Context, js jetstream.JetStream) error {
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      constants.ObservationStream,
		Subjects:  []string{constants.ObservationSubjectPrefix + ".>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.WorkQueuePolicy,
		MaxAge:    constants.ObservationStreamMaxAge,
	}); err != nil {
		return fmt.Errorf("ensure observation stream: %w", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      constants.ObservationDLQStream,
		Subjects:  []string{constants.ObservationSubjectPrefix + ".dlq"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    constants.ObservationDLQMaxAge,
	}); err != nil {
		return fmt.Errorf("ensure observation dlq stream: %w", err)
	}
	return nil
}

package wiring

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

// observationEmitterAdapter 实现 agentport.ObservationEmitter：把执行完成
// 引用事件序列化后发布到 evaluation.observe.{tenant} JetStream subject。
// best-effort 语义由调用方（agent application）保证，这里只负责可靠编解码与
// 发布失败透传（发布有独立超时预算，不占用 agent 请求时间预算）。
type observationEmitterAdapter struct {
	js     jetstream.JetStream
	logger *zap.Logger
}

func (a *observationEmitterAdapter) Emit(ctx context.Context, evt port.ObservationEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal observation event: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, constants.ObservationPublishTimeout)
	defer cancel()
	subject := constants.ObservationSubjectPrefix + "." + evt.TenantID
	if _, err := a.js.Publish(pctx, subject, data); err != nil {
		return fmt.Errorf("publish observation event: %w", err)
	}
	return nil
}

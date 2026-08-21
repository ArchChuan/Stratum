package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// jsPublisher 是发布器依赖的最小 JetStream 发布面（便于单测 fake）。
type jsPublisher interface {
	Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// NATSExtractionPublisher 把对话事实提取任务发布到
// memory.extraction.{tenant}，Msg-Id=message_id 保证 JetStream 去重。
// 生命周期（重试/DLQ）由消费侧 ack/MaxDeliver 与 memory.dlq 承载。
type NATSExtractionPublisher struct {
	js     jsPublisher
	logger *zap.Logger
}

func NewNATSExtractionPublisher(js jsPublisher, logger *zap.Logger) *NATSExtractionPublisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NATSExtractionPublisher{js: js, logger: logger}
}

// Enqueue 实现 port.ExtractionQueue：发布任务到 NATS。发布失败向上传播
// （Redis buffer flush 的调用方按显式降级语义处理）。
func (p *NATSExtractionPublisher) Enqueue(ctx context.Context, tenantID string, task *port.ExtractionTask) error {
	if p == nil || p.js == nil {
		return fmt.Errorf("extraction publisher: NATS not configured")
	}
	if task == nil {
		return fmt.Errorf("extraction publisher: task is nil")
	}
	if task.MessageID == "" {
		return fmt.Errorf("extraction publisher: message_id is empty")
	}
	if task.TraceID == "" {
		task.TraceID = uuid.NewString()
	}
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("extraction publisher: marshal task: %w", err)
	}
	subject := fmt.Sprintf("%s.%s", constants.MemoryExtractionSubject, tenantID)
	pubCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
	defer cancel()
	if _, err := p.js.Publish(pubCtx, subject, data, jetstream.WithMsgID(task.MessageID)); err != nil {
		return fmt.Errorf("extraction publisher: publish task: %w", err)
	}
	p.logger.Debug("memory.extraction.published",
		zap.String("tenant_id", tenantID),
		zap.String("message_id", task.MessageID),
		zap.String("trace_id", task.TraceID))
	return nil
}

// NATSReflectionPublisher 把工具轨迹反思任务发布到 memory.reflection.{tenant}，
// Msg-Id=execution_id 保证同一任务只入队一次。
type NATSReflectionPublisher struct {
	js     jsPublisher
	logger *zap.Logger
}

func NewNATSReflectionPublisher(js jsPublisher, logger *zap.Logger) *NATSReflectionPublisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NATSReflectionPublisher{js: js, logger: logger}
}

// Enqueue 发布一条反思任务；发布失败向上传播（agent 主路径按 fail-open
// 显式降级语义处理）。
func (p *NATSReflectionPublisher) Enqueue(ctx context.Context, task *port.ReflectionTask) error {
	if p == nil || p.js == nil {
		return fmt.Errorf("reflection publisher: NATS not configured")
	}
	if task == nil || task.ExecutionID == "" {
		return fmt.Errorf("reflection publisher: task execution_id is empty")
	}
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("reflection publisher: marshal task: %w", err)
	}
	subject := fmt.Sprintf("%s.%s", constants.MemoryReflectionSubject, task.TenantID)
	pubCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
	defer cancel()
	if _, err := p.js.Publish(pubCtx, subject, data, jetstream.WithMsgID(task.ExecutionID)); err != nil {
		return fmt.Errorf("reflection publisher: publish task: %w", err)
	}
	p.logger.Debug("memory.reflection.published",
		zap.String("tenant_id", task.TenantID),
		zap.String("execution_id", task.ExecutionID))
	return nil
}

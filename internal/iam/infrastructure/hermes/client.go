// Package hermes provides Hermes event bus integration.
package hermes

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

// queueNamePrefix 是 hermes 事件订阅的 queue group 名前缀。
// 限制：core NATS 订阅没有 durable 语义，所有实例离线期间发布的消息会丢失；
// 需要不丢消息时必须迁移到 JetStream durable consumer（参考 memory pipeline）。
// queue group 只提供在线成员的负载均衡与故障转移。
const queueNamePrefix = "hermes."

type Event struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
	Source    string      `json:"source"`
}

type EventHandler func(event *Event) error

// Client 消费 hermes 事件。连接由调用方提供（通常是
// pkg/messaging/nats.Connect 创建的平台共享连接），Client 不拥有连接，
// Close 不会关闭连接，连接生命周期由调用方（wiring.Storage）管理。
type Client struct {
	conn *nats.Conn
	// 每个事件类型只维护一个 queue subscription；同类型的多个 handler
	// 由该订阅统一派发，保持进程内 fan-out 的同时让跨实例订阅共享
	// 同一 queue group（负载均衡 + 故障转移）。
	subscriptions map[string]*nats.Subscription
	handlers      map[string][]EventHandler
	mu            sync.RWMutex
	logger        *zap.Logger
	metrics       observability.MetricsProvider
}

func NewClient(nc *nats.Conn, logger *zap.Logger, metrics observability.MetricsProvider) (*Client, error) {
	if nc == nil {
		return nil, fmt.Errorf("hermes: nil nats connection")
	}
	return &Client{
		conn:          nc,
		subscriptions: make(map[string]*nats.Subscription),
		handlers:      make(map[string][]EventHandler),
		logger:        logger,
		metrics:       metrics,
	}, nil
}

func (c *Client) Publish(event *Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		c.logger.Error("failed to marshal event", zap.Error(err))
		return err
	}

	subject := fmt.Sprintf("events.%s", event.Type)
	if err := c.conn.Publish(subject, data); err != nil {
		c.logger.Error("failed to publish event", zap.String("type", event.Type), zap.Error(err))
		c.metrics.IncHermesEventProcessed(event.Type, "publish_error")
		return err
	}

	c.metrics.IncHermesEvent(event.Type)
	c.logger.Debug("event published", zap.String("type", event.Type), zap.String("source", event.Source))
	return nil
}

// Subscribe 为 eventType 注册 handler。同一事件类型只创建一条 queue 订阅，
// 后续 Subscribe 只追加 handler——重复注册相同 queue group 的订阅会互相
// 抢消息，破坏进程内 fan-out。
func (c *Client) Subscribe(eventType string, handler EventHandler) error {
	subject := fmt.Sprintf("events.%s", eventType)

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.subscriptions[eventType]; exists {
		c.handlers[eventType] = append(c.handlers[eventType], handler)
		return nil
	}

	sub, err := c.conn.QueueSubscribe(subject, queueNamePrefix+eventType, c.dispatch(eventType))
	if err != nil {
		return fmt.Errorf("hermes: queue subscribe %s: %w", subject, err)
	}
	c.subscriptions[eventType] = sub
	c.handlers[eventType] = append(c.handlers[eventType], handler)

	c.logger.Info("subscribed to event",
		zap.String("type", eventType),
		zap.String("queue", queueNamePrefix+eventType))
	return nil
}

// dispatch 返回 eventType 的订阅回调。外层 recover 是最后防线：NATS 在
// 独立 goroutine 中调用回调，任何未恢复的 panic 都会直接崩溃整个进程。
func (c *Client) dispatch(eventType string) func(msg *nats.Msg) {
	return func(msg *nats.Msg) {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("hermes: event handler panic",
					zap.Any("event", eventType),
					zap.Error(fmt.Errorf("panic: %v", r)),
					zap.Stack("stack"))
				c.metrics.IncHermesEventProcessed(eventType, "panic")
			}
		}()

		var event Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			c.logger.Error("failed to unmarshal event", zap.String("type", eventType), zap.Error(err))
			c.metrics.IncHermesEventProcessed(eventType, "unmarshal_error")
			return
		}

		c.mu.RLock()
		handlers := c.handlers[eventType]
		c.mu.RUnlock()

		for _, h := range handlers {
			c.runHandler(eventType, h, &event)
		}
	}
}

// runHandler 执行单个 handler。单独 recover，保证一个 handler panic
// 不会中断同类型其他 handler 的派发。
func (c *Client) runHandler(eventType string, h EventHandler, event *Event) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("hermes: event handler panic",
				zap.Any("event", eventType),
				zap.Error(fmt.Errorf("panic: %v", r)),
				zap.Stack("stack"))
			c.metrics.IncHermesEventProcessed(eventType, "handler_panic")
		}
	}()
	if err := h(event); err != nil {
		c.logger.Error("event handler error", zap.String("type", eventType), zap.Error(err))
		c.metrics.IncHermesEventProcessed(eventType, "handler_error")
		return
	}
	c.metrics.IncHermesEventProcessed(eventType, "ok")
}

// Close 释放客户端。连接由调用方拥有并关闭（共享连接可能仍在被其他
// 组件使用），这里只做清理日志。
func (c *Client) Close() {
	c.logger.Info("hermes client closed")
}

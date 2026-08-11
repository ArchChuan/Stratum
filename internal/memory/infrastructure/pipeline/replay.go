package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// MaxDLQReplay 单条死信事件的最大重放次数（防重放风暴）。
const MaxDLQReplay = 3

const (
	// replayHeaderReplayed 标记事件已重放；replayHeaderReplayCount 记录累计重放次数。
	replayHeaderReplayed    = "Replayed"
	replayHeaderReplayCount = "ReplayCount"
)

const (
	// dlqReplayFetchBatch 单次拉取 DLQ 事件数；dlqReplayFetchWait 为空批等待上限。
	dlqReplayFetchBatch = 64
	dlqReplayFetchWait  = 500 * time.Millisecond
)

// ReplayService 定向重放 DLQ 事件回原始 raw subject（error_code 过滤）。
// tenantID 一律从事件 TenantID 字段派生，调用方无权指定——防跨租户重放。
type ReplayService struct {
	js     jetstream.JetStream
	logger *zap.Logger
}

// NewReplayService 构造重放服务。共享 NATS 连接的生命周期归 wiring，
// ReplayService 不持有 goroutine，无需关闭代码。
func NewReplayService(nc *nats.Conn, logger *zap.Logger) (*ReplayService, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream.New: %w", err)
	}
	return &ReplayService{js: js, logger: logger}, nil
}

// ReplayResult 单次重放操作的汇总。
type ReplayResult struct {
	Total    int `json:"total"`
	Replayed int `json:"replayed"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// ReplayByErrorCode 从 DLQ stream 拉取全部事件，error_code 匹配、payload 非空
// 且未超重放上限的，重发回 memory.raw.<tenant>。单条失败只计入 Failed（不中断）；
// 只有 stream 读取失败才返回 error。
//
// 幂等：raw publish 复用 replay:<StreamSequence> MsgID，JetStream dedup 窗口内
// 重复调用被去重，不会产生新消息。重放标记以同 dedupID 重发布回 DLQ subject：
// 窗口内被去重（原消息不落地新副本），窗口外落地为带 Replayed/ReplayCount
// header 的新副本，由 ReplayCount 上限兜底终止自喂循环。
func (s *ReplayService) ReplayByErrorCode(ctx context.Context, errorCode string) (ReplayResult, error) {
	result := ReplayResult{}
	consumer, err := s.js.CreateOrUpdateConsumer(ctx, constants.MemoryDLQStream, jetstream.ConsumerConfig{
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
		FilterSubject: constants.MemoryDLQSubject + ".>",
	})
	if err != nil {
		return result, fmt.Errorf("create dlq replay consumer: %w", err)
	}
	for {
		batch, err := consumer.Fetch(dlqReplayFetchBatch, jetstream.FetchMaxWait(dlqReplayFetchWait))
		if err != nil {
			return result, fmt.Errorf("fetch dlq: %w", err)
		}
		received := 0
		for msg := range batch.Messages() {
			received++
			s.replayOne(ctx, msg, errorCode, &result)
		}
		if received == 0 {
			return result, nil
		}
	}
}

func (s *ReplayService) replayOne(ctx context.Context, msg jetstream.Msg, errorCode string, result *ReplayResult) {
	var ev DeadLetterEvent
	if err := json.Unmarshal(msg.Data(), &ev); err != nil {
		result.Skipped++
		s.logger.Warn("memory.dlq.replay.unmarshal_failed", zap.Error(err))
		return
	}
	if ev.ErrorCode != errorCode {
		return
	}
	result.Total++
	replayCount := replayCountOf(msg.Headers())
	if len(ev.Payload) == 0 || replayCount >= MaxDLQReplay {
		result.Skipped++
		return
	}

	publishCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
	defer cancel()
	rawSubject := fmt.Sprintf("%s.%s", constants.MemoryRawSubject, ev.TenantID)
	replayMsgID := fmt.Sprintf("replay:%d", ev.StreamSequence)
	if _, err := s.js.Publish(publishCtx, rawSubject, ev.Payload, jetstream.WithMsgID(replayMsgID)); err != nil {
		result.Failed++
		s.logger.Error("memory.dlq.replay.publish_raw_failed",
			zap.String("tenant_id", ev.TenantID), zap.Error(err))
		return
	}
	// 先发布后标记：标记失败只计入 Failed，不推进 ReplayCount，
	// 下次重试仍可发布（raw 侧同 MsgID 在窗口内去重，不会重复消费）。
	mark := &nats.Msg{
		Subject: fmt.Sprintf("%s.%s", constants.MemoryDLQSubject, ev.TenantID),
		Data:    msg.Data(),
		Header: nats.Header{
			replayHeaderReplayed:    {"true"},
			replayHeaderReplayCount: {strconv.Itoa(replayCount + 1)},
		},
	}
	if _, err := s.js.PublishMsg(publishCtx, mark, jetstream.WithMsgID(deadLetterDedupID(ev))); err != nil {
		result.Failed++
		s.logger.Error("memory.dlq.replay.mark_failed",
			zap.String("tenant_id", ev.TenantID), zap.Error(err))
		return
	}
	result.Replayed++
	s.logger.Info("memory.dlq.replay.success",
		zap.String("tenant_id", ev.TenantID),
		zap.String("error_code", errorCode),
		zap.Uint64("stream_sequence", ev.StreamSequence))
}

// replayCountOf 读取消息 header 中的重放次数，缺失或非法按 0 计。
func replayCountOf(h nats.Header) int {
	if v := h.Get(replayHeaderReplayCount); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

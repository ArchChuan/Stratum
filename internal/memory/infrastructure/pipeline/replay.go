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
// 幂等：raw publish 复用 replay:<TenantID>:<OriginalStream>:<StreamSequence>
// MsgID，JetStream dedup 窗口内重复调用被去重，不会产生新消息；去重命中时
// 该事件计 Skipped 而非 Replayed，避免谎报。重放标记以
// dlq-mark:<TenantID>:<OriginalStream>:<StreamSequence>:<目标count> 为 MsgID
// 重发布回 DLQ subject——含目标 count 使标记在 dedup 窗口内也能落地为新副本
// （带 Replayed/ReplayCount header），ReplayCount 每次重放真实推进，直至
// ReplayCount 上限兜底终止副本链（收敛在 count=MaxDLQReplay）。
// 原消息本身不退役（AckNone 设计使然）：持久失败事件在运维持续调用时每个
// dedup 窗口至多被 re-feed 一次，re-feed 速率由 raw dedup 窗口硬限，天然防风暴。
func (s *ReplayService) ReplayByErrorCode(ctx context.Context, errorCode string) (ReplayResult, error) {
	result := ReplayResult{}
	// 快照语义：只处理调用开始时已在 DLQ 的消息。重放标记在调用期间落地为新
	// 副本，若也被本次消费会形成单次调用内的自喂级联（count 一次冲到上限、
	// Total/Skipped 记账失真）；留给下次调用处理，ReplayCount 按调用逐步推进。
	snapshotSeq, err := s.dlqSnapshotSeq(ctx)
	if err != nil {
		return result, err
	}
	consumer, err := s.js.CreateOrUpdateConsumer(ctx, constants.MemoryDLQStream, jetstream.ConsumerConfig{
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
		FilterSubject: constants.MemoryDLQSubject + ".>",
	})
	if err != nil {
		return result, fmt.Errorf("create dlq replay consumer: %w", err)
	}
	return s.replaySnapshot(ctx, consumer, snapshotSeq, errorCode, result)
}

// dlqSnapshotSeq 取 DLQ stream 的当前最后序列号，作为本次重放的快照边界。
func (s *ReplayService) dlqSnapshotSeq(ctx context.Context) (uint64, error) {
	stream, err := s.js.Stream(ctx, constants.MemoryDLQStream)
	if err != nil {
		return 0, fmt.Errorf("get dlq stream: %w", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("get dlq stream info: %w", err)
	}
	return info.State.LastSeq, nil
}

// replaySnapshot 拉取并重放快照内的 DLQ 消息；调用期间新落地的重放标记
// （seq > snapshotSeq）不处理，留给下次调用。
func (s *ReplayService) replaySnapshot(ctx context.Context, consumer jetstream.Consumer, snapshotSeq uint64, errorCode string, result ReplayResult) (ReplayResult, error) {
	for {
		batch, err := consumer.Fetch(dlqReplayFetchBatch, jetstream.FetchMaxWait(dlqReplayFetchWait))
		if err != nil {
			return result, fmt.Errorf("fetch dlq: %w", err)
		}
		received := 0
		beyondSnapshot := false
		for msg := range batch.Messages() {
			received++
			if beyondSnapshotSeq(msg, snapshotSeq) {
				beyondSnapshot = true
				continue
			}
			s.replayOne(ctx, msg, errorCode, &result)
		}
		if received == 0 || beyondSnapshot {
			return result, nil
		}
	}
}

// beyondSnapshotSeq 判断消息是否落在本次调用快照之外（调用期间新落地）。
func beyondSnapshotSeq(msg jetstream.Msg, snapshotSeq uint64) bool {
	meta, err := msg.Metadata()
	return err == nil && meta.Sequence.Stream > snapshotSeq
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
	// MsgID 含租户与原始流：不同租户/不同流相同 seq 的事件在窗口内互不去重。
	ack, err := s.js.Publish(publishCtx, rawSubject, ev.Payload, jetstream.WithMsgID(rawReplayMsgID(ev)))
	if err != nil {
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
	// mark MsgID 含目标 count：窗口内也能落地为新副本，ReplayCount 真实推进；
	// 与原消息 deadLetterDedupID（dlq:...）不同前缀，首轮标记不会被去重吞掉。
	if _, err := s.js.PublishMsg(publishCtx, mark, jetstream.WithMsgID(markReplayMsgID(ev, replayCount+1))); err != nil {
		result.Failed++
		s.logger.Error("memory.dlq.replay.mark_failed",
			zap.String("tenant_id", ev.TenantID), zap.Error(err))
		return
	}
	// raw 侧被 dedup：该事件在窗口内已 re-feed，本次不产生新消息——计 Skipped
	// 而非 Replayed，避免 API 谎报；mark 已发布，ReplayCount 照常推进。
	if ack != nil && ack.Duplicate {
		result.Skipped++
		return
	}
	result.Replayed++
	s.logger.Info("memory.dlq.replay.success",
		zap.String("tenant_id", ev.TenantID),
		zap.String("error_code", errorCode),
		zap.Uint64("stream_sequence", ev.StreamSequence))
}

// rawReplayMsgID 生成 raw 重放发布的 dedup MsgID。含租户与原始流，
// 保证不同租户/不同流的相同 seq 在窗口内互不去重（防碰撞）；
// 同一事件重复重放则复用同 ID，窗口内被服务器静默去重。
func rawReplayMsgID(ev DeadLetterEvent) string {
	return fmt.Sprintf("replay:%s:%s:%d", ev.TenantID, ev.OriginalStream, ev.StreamSequence)
}

// markReplayMsgID 生成重放标记的 dedup MsgID。targetCount 是本次标记试图写入
// 的 ReplayCount 值：含 count 后窗口内每次重放的标记都能落地（与原消息的
// dlq: 前缀及上次标记的 count 均不同），ReplayCount 逐步推进至上限。
func markReplayMsgID(ev DeadLetterEvent, targetCount int) string {
	return fmt.Sprintf("dlq-mark:%s:%s:%d:%d", ev.TenantID, ev.OriginalStream, ev.StreamSequence, targetCount)
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

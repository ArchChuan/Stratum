// Package stream 实现 Agent 执行输出流的端口。
//
// 租户命名空间在这里收口：调用方只给 executionID / generation，key 由本包
// 经 tenantnaming.TenantKey 构造，因此调用方永远构造不出裸 key，也不可能
// 绕过租户边界（spec D4）。
package stream

import (
	"context"
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
	"go.uber.org/zap"
)

// 流 key 中「流」段的段名，最终 key 为 agent:{tenant}:stream:{executionID}:{gen}。
const keySegmentStream = "stream"

// AgentStreamStore 是 port.AgentStreamStore 的 Redis 实现。
type AgentStreamStore struct {
	stream  *redis.StreamStore
	ttl     time.Duration
	maxLen  int64
	replay  int64
	readBlk time.Duration
	logger  *zap.Logger
}

var _ port.AgentStreamStore = (*AgentStreamStore)(nil)

// NewAgentStreamStore 用默认时长装配；测试可用 NewAgentStreamStoreWith。
func NewAgentStreamStore(s *redis.StreamStore, logger *zap.Logger) *AgentStreamStore {
	return NewAgentStreamStoreWith(s, constants.AgentStreamTTL, constants.AgentStreamMaxLen,
		constants.AgentStreamReplayBatch, constants.AgentStreamReadBlock, logger)
}

// NewAgentStreamStoreWith 注入时长与容量，供跨实例测试把秒级等待压到毫秒级。
// logger 为 nil 时归一化为 no-op（与 application 侧 deps.Logger == nil 同型）：
// 续期失败的 WARN 是「key 可能没有 TTL」这一分支的唯一留痕，不能因忘传 logger
// 而重新变成静默。
func NewAgentStreamStoreWith(
	s *redis.StreamStore, ttl time.Duration, maxLen, replay int64, readBlock time.Duration,
	logger *zap.Logger,
) *AgentStreamStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AgentStreamStore{
		stream: s, ttl: ttl, maxLen: maxLen, replay: replay, readBlk: readBlock, logger: logger,
	}
}

func (s *AgentStreamStore) Append(
	ctx context.Context, executionID string, generation int, e port.StreamEntry,
) (string, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return "", err
	}
	id, err := s.stream.AppendEvent(ctx, key, s.maxLen, e.Event, e.Payload)
	if err != nil {
		return "", err
	}
	// TTL 在写路径搭车刷新，不额外起定时器。续期失败不阻断写入——流本身是尽力而为
	// 的显示缓冲，丢一次续期最多让流早一小时过期，而这次写入对调用方已经成功。
	//
	// 但降级必须留痕，不能 `_ =` 静默吞掉：持续失败有两条都不可接受的后果——
	// 运行中的 key 被提前过期删除（回放断档），或 key 从未拿到 TTL 而永不过期
	// （内存按执行数累积）。WARN 而非 ERROR：单次失败确实是可容忍的降级。
	if err := s.stream.Expire(ctx, key, s.ttl); err != nil {
		s.logger.Warn("agent stream: ttl refresh failed",
			zap.String("execution_id", executionID),
			zap.Int("generation", generation),
			zap.Error(err))
	}
	return id, nil
}

func (s *AgentStreamStore) Replay(
	ctx context.Context, executionID string, generation int,
) ([]port.StreamEntry, error) {
	return s.ReplayAfter(ctx, executionID, generation, "")
}

func (s *AgentStreamStore) ReplayAfter(
	ctx context.Context, executionID string, generation int, afterID string,
) ([]port.StreamEntry, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return nil, err
	}
	entries, err := s.stream.Range(ctx, key, afterID, s.replay)
	if err != nil {
		return nil, err
	}
	return toPortEntries(entries), nil
}

func (s *AgentStreamStore) FirstID(ctx context.Context, executionID string, generation int) (string, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return "", err
	}
	return s.stream.FirstID(ctx, key)
}

func (s *AgentStreamStore) Tail(
	ctx context.Context, executionID string, generation int, afterID string, block int,
) ([]port.StreamEntry, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return nil, err
	}
	d := s.readBlk
	if block > 0 {
		d = time.Duration(block) * time.Millisecond
	}
	entries, err := s.stream.Tail(ctx, key, afterID, s.replay, d)
	if err != nil {
		return nil, err
	}
	return toPortEntries(entries), nil
}

func (s *AgentStreamStore) RefreshTTL(ctx context.Context, executionID string, generation int) error {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return err
	}
	return s.stream.Expire(ctx, key, s.ttl)
}

func (s *AgentStreamStore) Delete(ctx context.Context, executionID string, generation int) error {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return err
	}
	return s.stream.Delete(ctx, key)
}

func (s *AgentStreamStore) key(ctx context.Context, executionID string, generation int) (string, error) {
	return tenantnaming.TenantKey(ctx, tenantnaming.RedisKeyPrefix,
		keySegmentStream, executionID, strconv.Itoa(generation))
}

func toPortEntries(entries []redis.StreamEntry) []port.StreamEntry {
	out := make([]port.StreamEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, port.StreamEntry{ID: e.ID, Event: e.Event, Payload: e.Payload})
	}
	return out
}

package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// StreamEntry 是流中的一条条目。ID 是 Redis 生成的 entry ID（形如
// 1726483200000-37），逐字作为 SSE 的 id: 行，客户端原样回传即可续传。
type StreamEntry struct {
	ID      string
	Event   string
	Payload string
}

// 条目使用两个固定字段名。字段名固定而非动态，是为了让 XADD/XREAD 的取值
// 形状与在线 wire 格式一一对应：Event 为空即整行省略 event:，零转换。
const (
	streamFieldEvent   = "e"
	streamFieldPayload = "d"

	// streamNonBlocking 是 XReadArgs.Block 的哨兵值：go-redis 只在 Block < 0 时
	// 完全省略 BLOCK 参数，而 Block == 0 会被原样拼成 `BLOCK 0`——Redis 语义是
	// 「永不超时」，连接会被无限期占用。
	streamNonBlocking = -1
)

// StreamStore 是 Redis Streams 的薄封装。它只做 IO——key 由调用方构造
// （Agent 侧经 tenantnaming.TenantKey 生成），本类型不承担租户语义。
type StreamStore struct {
	client *goredis.Client
}

func NewStreamStore(client *goredis.Client) *StreamStore {
	return &StreamStore{client: client}
}

// AppendEvent 追加一条带事件名的条目并返回 Redis 生成的 entry ID。
func (s *StreamStore) AppendEvent(
	ctx context.Context, key string, maxLen int64, event, payload string,
) (string, error) {
	return s.append(ctx, key, maxLen, []any{streamFieldEvent, event, streamFieldPayload, payload})
}

// Append 追加一条不带事件名的条目（兼容今天无 event: 行的在线格式）。
func (s *StreamStore) Append(ctx context.Context, key string, maxLen int64, payload string) (string, error) {
	return s.append(ctx, key, maxLen, []any{streamFieldPayload, payload})
}

func (s *StreamStore) append(ctx context.Context, key string, maxLen int64, values []any) (string, error) {
	args := &goredis.XAddArgs{Stream: key, ID: "*", Values: values}
	if maxLen > 0 {
		// 近似（~）而非精确裁剪是刻意的：精确裁剪要逐条驱逐，代价高，而这个流
		// 的定位是尽力而为的显示缓冲，不是事实源（spec §3）。
		args.MaxLen = maxLen
		args.Approx = true
	}
	id, err := s.client.XAdd(ctx, args).Result()
	if err != nil {
		return "", fmt.Errorf("redis: xadd %q: %w", key, err)
	}
	return id, nil
}

// Range 返回 afterID 之后的条目。afterID 为空时从头全量回放，count <= 0 表示
// 不限条数：XRangeN 会无条件拼出 `COUNT <n>`，而真实 Redis 对 `COUNT 0` 返回的是
// null array（被客户端映射成 redis.Nil），所以不限条数必须走不带 COUNT 的 XRange。
// 游标是「已渲染到的最后一条」，因此范围**排他**——包含它会让重连后的第一格重复渲染。
func (s *StreamStore) Range(ctx context.Context, key, afterID string, count int64) ([]StreamEntry, error) {
	start := "-"
	if afterID != "" {
		start = "(" + afterID
	}
	var (
		msgs []goredis.XMessage
		err  error
	)
	if count > 0 {
		msgs, err = s.client.XRangeN(ctx, key, start, "+", count).Result()
	} else {
		msgs, err = s.client.XRange(ctx, key, start, "+").Result()
	}
	if err != nil {
		return nil, fmt.Errorf("redis: xrange %q: %w", key, err)
	}
	return toEntries(msgs), nil
}

// FirstID 返回流中最老条目的 ID；流不存在或为空时返回 ""。用于判定游标是否
// 因裁剪而早于现存最老条目（缺口检测）。
func (s *StreamStore) FirstID(ctx context.Context, key string) (string, error) {
	msgs, err := s.client.XRangeN(ctx, key, "-", "+", 1).Result()
	if err != nil {
		return "", fmt.Errorf("redis: xrange first %q: %w", key, err)
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].ID, nil
}

// Tail 从 afterID 之后读取至多 block 时长的新条目。block <= 0 表示非阻塞读——
// Go 的零值惯用法必须落在「立刻返回」上，而不是落到 Redis 的 `BLOCK 0`（永不超时）。
// 无新条目时返回空切片与 nil error——调用方据此检查 ctx 并写心跳，而不是当成失败；
// 空切片而非 nil 是契约的一部分，nil 序列化出去是 null，调用方拿到的是 []。
func (s *StreamStore) Tail(
	ctx context.Context, key, afterID string, count int64, block time.Duration,
) ([]StreamEntry, error) {
	if afterID == "" {
		afterID = "0-0"
	}
	if block <= 0 {
		block = streamNonBlocking
	}
	msgs, err := s.client.XRead(ctx, &goredis.XReadArgs{
		Streams: []string{key, afterID},
		Count:   count,
		Block:   block,
	}).Result()
	if errors.Is(err, goredis.Nil) {
		return []StreamEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis: xread %q: %w", key, err)
	}
	if len(msgs) == 0 {
		return []StreamEntry{}, nil
	}
	return toEntries(msgs[0].Messages), nil
}

func (s *StreamStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := s.client.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("redis: expire %q: %w", key, err)
	}
	return nil
}

func (s *StreamStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis: del %q: %w", key, err)
	}
	return nil
}

func toEntries(msgs []goredis.XMessage) []StreamEntry {
	out := make([]StreamEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, StreamEntry{
			ID:      m.ID,
			Event:   stringField(m.Values, streamFieldEvent),
			Payload: stringField(m.Values, streamFieldPayload),
		})
	}
	return out
}

// stringField 容忍缺字段与 nil 值：历史条目（无 e 字段）与运行期字段缺失都
// 必须降级为空串，而不是 panic 或记住一个 "nil" 字面量。
func stringField(values map[string]any, key string) string {
	v, ok := values[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

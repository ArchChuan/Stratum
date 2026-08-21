package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestApplicationLayerDoesNotImportRedisClient(t *testing.T) {
	files := []string{
		"memory_service_v2.go",
		"message_buffer.go",
		"buffer_scanner.go",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(".", name))
			assert.NoError(t, err)
			assert.NotContains(t, string(data), "github.com/redis/go-redis/v9")
			assert.False(t, strings.Contains(string(data), "*redis.Client"))
		})
	}
}

func TestMessageBuffer_BufferMessage_NoRedis(t *testing.T) {
	queue := new(MockExtractionQueue)
	buffer := NewMessageBuffer(nil, queue)

	req := &BufferMessageRequest{
		TenantID:       "tenant1",
		UserID:         "user1",
		AgentID:        "agent1",
		ConversationID: "conv1",
		MessageID:      "msg1",
		Role:           "user",
		Content:        "test",
		CreatedAt:      time.Now(),
	}

	err := buffer.BufferMessage(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis client not configured")
}

func TestMessageBuffer_BufferMessageFlushesAtThresholdAndDeletesBuffer(t *testing.T) {
	store := newFakeMessageBufferStore()
	queue := new(MockExtractionQueue)
	queue.On("Enqueue", mock.Anything, "tenant1", mock.MatchedBy(func(task *port.ExtractionTask) bool {
		var messages []map[string]string
		if err := json.Unmarshal([]byte(task.Content), &messages); err != nil {
			return false
		}
		return task.TenantID == "tenant1" &&
			task.UserID == "user1" &&
			task.AgentID == "agent1" &&
			task.ConversationID == "conv1" &&
			task.Scope == "session" &&
			task.MessageID == "msg1" &&
			len(messages) == constants.MemoryBufferFlushSize &&
			messages[0]["role"] == "user" &&
			messages[0]["content"] == "User preference content item number 1 in conversation"
	})).Return(nil).Once()

	buffer := NewMessageBuffer(store, queue)
	for i := 1; i <= constants.MemoryBufferFlushSize; i++ {
		req := &BufferMessageRequest{
			TenantID:       "tenant1",
			UserID:         "user1",
			AgentID:        "agent1",
			ConversationID: "conv1",
			Scope:          "session",
			MessageID:      fmt.Sprintf("msg%d", i),
			Role:           "user",
			Content:        fmt.Sprintf("User preference content item number %d in conversation", i),
			CreatedAt:      time.Now(),
		}
		require.NoError(t, buffer.BufferMessage(context.Background(), req))
	}

	queue.AssertExpectations(t)
	assert.True(t, store.deleted["memory:buffer:tenant1:user1:agent1:conv1"])
	assert.True(t, store.deleted["memory:buffer:meta:tenant1:user1:agent1:conv1"])
	assert.Empty(t, store.lists["memory:buffer:tenant1:user1:agent1:conv1"])
}

// Integration tests requiring Redis are skipped in -short mode
func TestBufferMessage_FlushAtK5(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	// TODO: implement with real Redis in integration tests
}

func TestBufferMessage_FlushAt2Min(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	// TODO: implement with real Redis in integration tests
}

type fakeMessageBufferStore struct {
	lists    map[string][]string
	hashes   map[string]map[string]string
	deleted  map[string]bool
	lockHeld map[string]string

	hSetNXErr  error
	hIncrByErr error
	hSetErr    error
	expireErr  error
	delErr     error
	removeErr  error
	lockErr    error

	// onLRangeAfter 在 LRange 返回快照之后触发，用于在测试中模拟
	// "flush 读快照后、清理前"恰好到达的新消息。
	onLRangeAfter func(key string)
}

func newFakeMessageBufferStore() *fakeMessageBufferStore {
	return &fakeMessageBufferStore{
		lists:   make(map[string][]string),
		hashes:  make(map[string]map[string]string),
		deleted: make(map[string]bool),
	}
}

func (s *fakeMessageBufferStore) RPush(_ context.Context, key string, value []byte) error {
	s.lists[key] = append(s.lists[key], string(value))
	return nil
}

func (s *fakeMessageBufferStore) Expire(_ context.Context, _ string, _ time.Duration) error {
	return s.expireErr
}

func (s *fakeMessageBufferStore) HSetNX(_ context.Context, key, field string, value any) error {
	if s.hSetNXErr != nil {
		return s.hSetNXErr
	}
	if s.hashes[key] == nil {
		s.hashes[key] = make(map[string]string)
	}
	if _, ok := s.hashes[key][field]; !ok {
		s.hashes[key][field] = value.(string)
	}
	return nil
}

func (s *fakeMessageBufferStore) HIncrBy(_ context.Context, key, field string, incr int64) (int64, error) {
	if s.hIncrByErr != nil {
		return 0, s.hIncrByErr
	}
	if s.hashes[key] == nil {
		s.hashes[key] = make(map[string]string)
	}
	var current int64
	_, _ = fmt.Sscan(s.hashes[key][field], &current)
	current += incr
	s.hashes[key][field] = fmt.Sprint(current)
	return current, nil
}

func (s *fakeMessageBufferStore) HSet(_ context.Context, key string, values ...any) error {
	if s.hSetErr != nil {
		return s.hSetErr
	}
	if s.hashes[key] == nil {
		s.hashes[key] = make(map[string]string)
	}
	for i := 0; i+1 < len(values); i += 2 {
		s.hashes[key][values[i].(string)] = values[i+1].(string)
	}
	return nil
}

func (s *fakeMessageBufferStore) LLen(_ context.Context, key string) (int64, error) {
	return int64(len(s.lists[key])), nil
}

func (s *fakeMessageBufferStore) LIndex(_ context.Context, key string, index int64) (string, bool, error) {
	if int(index) >= len(s.lists[key]) {
		return "", false, nil
	}
	return s.lists[key][index], true, nil
}

func (s *fakeMessageBufferStore) LRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	if start == 0 && stop == -1 {
		snapshot := append([]string(nil), s.lists[key]...)
		if s.onLRangeAfter != nil {
			s.onLRangeAfter(key)
		}
		return snapshot, nil
	}
	return nil, nil
}

func (s *fakeMessageBufferStore) Del(_ context.Context, keys ...string) error {
	if s.delErr != nil {
		return s.delErr
	}
	for _, key := range keys {
		s.deleted[key] = true
		delete(s.lists, key)
		delete(s.hashes, key)
	}
	return nil
}

// RemoveValues 镜像 Redis Lua 语义：按值删除所有匹配项；每个值仅当其实际
// 删除过（removed>0）才回扣一次 byte_size；列表清空时删除两个 key。
func (s *fakeMessageBufferStore) RemoveValues(_ context.Context, key, metaKey string, values []string, sizes []int64) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	for i, v := range values {
		removed := false
		kept := make([]string, 0, len(s.lists[key]))
		for _, item := range s.lists[key] {
			if item == v && !removed {
				removed = true
				continue
			}
			if item != v {
				kept = append(kept, item)
			}
		}
		s.lists[key] = kept
		if removed {
			if s.hashes[metaKey] == nil {
				s.hashes[metaKey] = make(map[string]string)
			}
			var current int64
			_, _ = fmt.Sscan(s.hashes[metaKey]["byte_size"], &current)
			current -= sizes[i]
			s.hashes[metaKey]["byte_size"] = fmt.Sprint(current)
		}
	}
	if len(s.lists[key]) == 0 {
		delete(s.lists, key)
		delete(s.hashes, metaKey)
		s.deleted[key] = true
		s.deleted[metaKey] = true
	}
	return nil
}

func (s *fakeMessageBufferStore) TryAcquireFlushLock(_ context.Context, key, token string, _ time.Duration) (bool, error) {
	if s.lockErr != nil {
		return false, s.lockErr
	}
	if s.lockHeld == nil {
		s.lockHeld = make(map[string]string)
	}
	if _, held := s.lockHeld[key]; held {
		return false, nil
	}
	s.lockHeld[key] = token
	return true, nil
}

func (s *fakeMessageBufferStore) ReleaseFlushLock(_ context.Context, key, token string) error {
	if s.lockHeld[key] == token {
		delete(s.lockHeld, key)
	}
	return nil
}

func (s *fakeMessageBufferStore) Scan(_ context.Context, _ uint64, _ string, _ int64) ([]string, uint64, error) {
	return nil, 0, nil
}

func (s *fakeMessageBufferStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	return s.hashes[key], nil
}

// TestMessageBuffer_MetaWriteFailure_FailsClosed pins that every Redis meta
// write (first_at, byte_size, last_at/scope, TTL) propagates its error: the
// flush thresholds and key TTL are driven by this state, so a silent failure
// would skew the counters or leak keys forever.
func TestMessageBuffer_MetaWriteFailure_FailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		fail    func(*fakeMessageBufferStore)
		wantErr string
	}{
		{"expire", func(s *fakeMessageBufferStore) { s.expireErr = errors.New("boom") }, "redis expire"},
		{"hsetnx", func(s *fakeMessageBufferStore) { s.hSetNXErr = errors.New("boom") }, "redis hsetnx"},
		{"hincrby", func(s *fakeMessageBufferStore) { s.hIncrByErr = errors.New("boom") }, "redis hincrby"},
		{"hset", func(s *fakeMessageBufferStore) { s.hSetErr = errors.New("boom") }, "redis hset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeMessageBufferStore()
			tc.fail(store)
			buffer := NewMessageBuffer(store, new(MockExtractionQueue))

			err := buffer.BufferMessage(context.Background(), &BufferMessageRequest{
				TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
				ConversationID: "conv1", MessageID: "msg1", Role: "user",
				Content: "some content", CreatedAt: time.Now(),
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestMessageBuffer_Flush_DeleteFailure_RetainsMessages pins that a failed
// Redis DEL in the quality-gate flush path keeps the batch in place for the
// next retry instead of silently dropping the low-value messages.
func TestMessageBuffer_Flush_DeleteFailure_RetainsMessages(t *testing.T) {
	store := newFakeMessageBufferStore()
	store.removeErr = errors.New("redis unavailable")
	buffer := NewMessageBuffer(store, new(MockExtractionQueue))

	var lastErr error
	key := "memory:buffer:tenant1:user1:agent1:conv1"
	for i := 1; i <= constants.MemoryBufferFlushSize; i++ {
		lastErr = buffer.BufferMessage(context.Background(), &BufferMessageRequest{
			TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
			ConversationID: "conv1", Scope: "session",
			MessageID: fmt.Sprintf("msg%d", i), Role: "user",
			Content: "ok", CreatedAt: time.Now(),
		})
	}
	require.Error(t, lastErr)
	assert.Contains(t, lastErr.Error(), "redis remove values")
	assert.Len(t, store.lists[key], constants.MemoryBufferFlushSize,
		"messages must stay in Redis after a failed delete")
	assert.Empty(t, store.deleted)
}

// TestMessageBuffer_FlushDoesNotLoseTailArrival 复现 flush 的
// LRange → Enqueue → 清理 窗口竞态：快照之后到达的新消息不得被整 key 删除。
func TestMessageBuffer_FlushDoesNotLoseTailArrival(t *testing.T) {
	store := newFakeMessageBufferStore()
	queue := new(MockExtractionQueue)
	buffer := NewMessageBuffer(store, queue)

	ctx := context.Background()
	key := "memory:buffer:tenant1:user1:agent1:conv1"
	queue.On("Enqueue", mock.Anything, "tenant1", mock.Anything).Return(nil).Times(2)

	// 前 4 条不触发 flush；第 5 条触发。flush 的 LRange 返回快照后注入一条
	// "恰好到达"的新消息——模拟单实例下 LRange 与清理之间的写入窗口。
	store.onLRangeAfter = func(k string) {
		_ = store.RPush(ctx, k, mustMarshalBufferMessage("tail", "tail content padding enough content padding enough padding", time.Now()))
	}

	for i := 1; i <= constants.MemoryBufferFlushSize; i++ {
		require.NoError(t, buffer.BufferMessage(ctx, &BufferMessageRequest{
			TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
			ConversationID: "conv1", Scope: "session",
			MessageID: fmt.Sprintf("msg%d", i), Role: "user",
			Content: "content padding enough", CreatedAt: time.Now(),
		}))
	}

	// 第 5 条触发 flush：tail 不在快照中，必须保留在 Redis 等待下一轮。
	require.Len(t, store.lists[key], 1, "tail 消息不得被整 key 删除")
	require.Contains(t, store.lists[key][0], "tail")

	// 下一轮 flush 把 tail 入队（不丢失）。
	store.onLRangeAfter = nil // 模拟窗口只出现一次，后续 flush 不再注入
	require.NoError(t, buffer.flush(ctx, key, "tenant1", "user1", "agent1", "conv1", "session"))
	queue.AssertNumberOfCalls(t, "Enqueue", 2)
	var secondTask *port.ExtractionTask
	for _, call := range queue.Calls {
		if task, ok := call.Arguments.Get(2).(*port.ExtractionTask); ok {
			secondTask = task
		}
	}
	require.NotNil(t, secondTask)
	assert.Contains(t, secondTask.Content, "tail")
	assert.Empty(t, store.lists[key])
}

// TestMessageBuffer_FlushSkipsWhenAnotherFlusherHoldsLock 验证单飞锁：
// 其他 flusher 正在排水时本轮必须跳过，既不入队也不清理。
func TestMessageBuffer_FlushSkipsWhenAnotherFlusherHoldsLock(t *testing.T) {
	store := newFakeMessageBufferStore()
	queue := new(MockExtractionQueue)
	buffer := NewMessageBuffer(store, queue)

	ctx := context.Background()
	key := "memory:buffer:tenant1:user1:agent1:conv1"
	store.lockHeld = map[string]string{key + ":flush": "other-flusher"}

	for i := 1; i <= constants.MemoryBufferFlushSize; i++ {
		require.NoError(t, buffer.BufferMessage(ctx, &BufferMessageRequest{
			TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
			ConversationID: "conv1", Scope: "session",
			MessageID: fmt.Sprintf("msg%d", i), Role: "user",
			Content: "content padding enough", CreatedAt: time.Now(),
		}))
	}

	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
	require.Len(t, store.lists[key], constants.MemoryBufferFlushSize,
		"锁被持有时 flush 必须跳过且保留消息，等待持锁者完成")
}

// TestMessageBuffer_FlushRemoveValuesFailure_RetainsMessages pin：Enqueue 成功
// 后值精确删除失败必须返回错误且保留消息（下轮重读 + Enqueue 幂等去重后补
// 删），不允许吞错导致消息永久滞留或丢失。
func TestMessageBuffer_FlushRemoveValuesFailure_RetainsMessages(t *testing.T) {
	store := newFakeMessageBufferStore()
	store.removeErr = errors.New("redis unavailable")
	queue := new(MockExtractionQueue)
	buffer := NewMessageBuffer(store, queue)

	ctx := context.Background()
	key := "memory:buffer:tenant1:user1:agent1:conv1"
	queue.On("Enqueue", mock.Anything, "tenant1", mock.Anything).Return(nil).Once()

	var lastErr error
	for i := 1; i <= constants.MemoryBufferFlushSize; i++ {
		lastErr = buffer.BufferMessage(ctx, &BufferMessageRequest{
			TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
			ConversationID: "conv1", Scope: "session",
			MessageID: fmt.Sprintf("msg%d", i), Role: "user",
			Content: "content padding enough", CreatedAt: time.Now(),
		})
	}
	require.Error(t, lastErr)
	assert.Contains(t, lastErr.Error(), "redis remove values")
	require.Len(t, store.lists[key], constants.MemoryBufferFlushSize,
		"删除失败后消息必须保留在 Redis（Enqueue 已幂等，下轮补删）")
}

func mustMarshalBufferMessage(messageID, content string, createdAt time.Time) []byte {
	data, _ := json.Marshal(&BufferMessageRequest{
		TenantID: "tenant1", UserID: "user1", AgentID: "agent1",
		ConversationID: "conv1", Scope: "session",
		MessageID: messageID, Role: "user",
		Content: content, CreatedAt: createdAt,
	})
	return data
}

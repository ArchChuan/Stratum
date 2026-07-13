package application

import (
	"context"
	"encoding/json"
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
	lists   map[string][]string
	hashes  map[string]map[string]string
	deleted map[string]bool
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
	return nil
}

func (s *fakeMessageBufferStore) HSetNX(_ context.Context, key, field string, value any) error {
	if s.hashes[key] == nil {
		s.hashes[key] = make(map[string]string)
	}
	if _, ok := s.hashes[key][field]; !ok {
		s.hashes[key][field] = value.(string)
	}
	return nil
}

func (s *fakeMessageBufferStore) HIncrBy(_ context.Context, key, field string, incr int64) (int64, error) {
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
		return append([]string(nil), s.lists[key]...), nil
	}
	return nil, nil
}

func (s *fakeMessageBufferStore) Del(_ context.Context, keys ...string) error {
	for _, key := range keys {
		s.deleted[key] = true
		delete(s.lists, key)
		delete(s.hashes, key)
	}
	return nil
}

func (s *fakeMessageBufferStore) Scan(_ context.Context, _ uint64, _ string, _ int64) ([]string, uint64, error) {
	return nil, 0, nil
}

func (s *fakeMessageBufferStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	return s.hashes[key], nil
}

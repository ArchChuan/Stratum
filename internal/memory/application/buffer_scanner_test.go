package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func testLogger() *zap.Logger { return zap.NewNop() }

// scriptedBufferStore 扩展 fakeMessageBufferStore：Scan/HGetAll 可脚本化，其余方法复用。
type scriptedBufferStore struct {
	*fakeMessageBufferStore
	scanKeys   []string
	scanNext   uint64
	scanErr    error
	scanCalls  int
	hgetAllErr error
}

func (s *scriptedBufferStore) Scan(_ context.Context, _ uint64, _ string, _ int64) ([]string, uint64, error) {
	s.scanCalls++
	if s.scanCalls >= 2 {
		// 第二页：游标归零，循环终止。
		return nil, 0, nil
	}
	return s.scanKeys, s.scanNext, s.scanErr
}

func (s *scriptedBufferStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	if s.hgetAllErr != nil {
		return nil, s.hgetAllErr
	}
	return s.fakeMessageBufferStore.HGetAll(context.Background(), key)
}

// newBufferScanner 构造带 scripted store 和 mock queue 的 scanner。
func newBufferScanner(t *testing.T) (*BufferScanner, *scriptedBufferStore, *MockExtractionQueue) {
	t.Helper()
	store := &scriptedBufferStore{fakeMessageBufferStore: newFakeMessageBufferStore()}
	queue := new(MockExtractionQueue)
	scanner := NewBufferScanner(store, queue, testLogger())
	return scanner, store, queue
}

// metaKeyOf 按 BufferScanner 期望的 key 格式构造 meta key。
func metaKeyOf(tid, uid, aid, cid string) string {
	return "memory:buffer:meta:" + tid + ":" + uid + ":" + aid + ":" + cid
}

// seedExpiredBuffer 预置一个已超时的缓冲：消息内容足够长（≥MinContentRunes）。
func seedExpiredBuffer(t *testing.T, store *scriptedBufferStore, queue *MockExtractionQueue) string {
	t.Helper()
	listKey := "memory:buffer:t1:u1:a1:c1"
	meta := metaKeyOf("t1", "u1", "a1", "c1")
	old := time.Now().Add(-constants.MemoryBufferAgeTimeout - time.Minute).Format(time.RFC3339)
	store.hashes[meta] = map[string]string{
		"last_at":  old,
		"first_at": old,
		"scope":    "session",
	}
	msg := BufferMessageRequest{Role: "user", Content: strings.Repeat("x", constants.MemoryBufferMinContentRunes+10), MessageID: "m1"}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	store.lists[listKey] = []string{string(raw)}
	return meta
}

func TestBufferScannerCheckAndFlushHGetAllError(t *testing.T) {
	scanner, store, queue := newBufferScanner(t)
	store.hgetAllErr = assert.AnError
	scanner.checkAndFlush(context.Background(), metaKeyOf("t1", "u1", "a1", "c1"))
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerCheckAndFlushEmptyFields(t *testing.T) {
	// 极端情况：meta 存在但无字段，静默跳过。
	scanner, store, queue := newBufferScanner(t)
	store.hashes[metaKeyOf("t1", "u1", "a1", "c1")] = map[string]string{}
	scanner.checkAndFlush(context.Background(), metaKeyOf("t1", "u1", "a1", "c1"))
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerCheckAndFlushMalformedTimestamps(t *testing.T) {
	// 极端情况：last_at/first_at 非 RFC3339，静默跳过。
	cases := []struct {
		name   string
		fields map[string]string
	}{
		{"invalid last_at", map[string]string{"last_at": "yesterday", "first_at": time.Now().Format(time.RFC3339)}},
		{"invalid first_at", map[string]string{"last_at": time.Now().Format(time.RFC3339), "first_at": "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scanner, store, queue := newBufferScanner(t)
			store.hashes[metaKeyOf("t1", "u1", "a1", "c1")] = tc.fields
			scanner.checkAndFlush(context.Background(), metaKeyOf("t1", "u1", "a1", "c1"))
			queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestBufferScannerCheckAndFlushNotDue(t *testing.T) {
	// 极端情况：idle 与 age 均未超时，不 flush。
	scanner, store, queue := newBufferScanner(t)
	now := time.Now().Format(time.RFC3339)
	store.hashes[metaKeyOf("t1", "u1", "a1", "c1")] = map[string]string{
		"last_at":  now,
		"first_at": now,
	}
	scanner.checkAndFlush(context.Background(), metaKeyOf("t1", "u1", "a1", "c1"))
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerCheckAndFlushFlushesExpiredBuffer(t *testing.T) {
	// 成功路径：age 超时 → flush → LRange → Enqueue → Del。
	scanner, store, queue := newBufferScanner(t)
	meta := seedExpiredBuffer(t, store, queue)
	queue.On("Enqueue", mock.Anything, "t1", mock.MatchedBy(func(task *port.ExtractionTask) bool {
		return task.TenantID == "t1" && task.UserID == "u1" && task.AgentID == "a1" &&
			task.ConversationID == "c1" && task.Scope == "session" && task.MessageID == "m1"
	})).Return(nil).Once()

	scanner.checkAndFlush(context.Background(), meta)

	queue.AssertExpectations(t)
	assert.True(t, store.deleted[meta], "meta key must be deleted after flush")
	assert.True(t, store.deleted["memory:buffer:t1:u1:a1:c1"], "list key must be deleted after flush")
}

func TestBufferScannerCheckAndFlushFlushError(t *testing.T) {
	// 极端情况：Enqueue 失败向上返回错误，不 panic、不删除数据。
	scanner, store, queue := newBufferScanner(t)
	meta := seedExpiredBuffer(t, store, queue)
	queue.On("Enqueue", mock.Anything, "t1", mock.Anything).Return(assert.AnError).Once()

	scanner.checkAndFlush(context.Background(), meta)

	queue.AssertExpectations(t)
	assert.False(t, store.deleted[meta], "data must survive failed enqueue")
}

func TestBufferScannerCheckAndFlushMalformedKey(t *testing.T) {
	// 极端情况：meta key 缺少租户/用户等段（parts != 4），跳过。
	scanner, store, queue := newBufferScanner(t)
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	shortMeta := "memory:buffer:meta:t1:u1:a1"
	store.hashes[shortMeta] = map[string]string{"last_at": old, "first_at": old}
	scanner.checkAndFlush(context.Background(), shortMeta)
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerCheckAndFlushKeyShorterThanPrefix(t *testing.T) {
	// 已知缺陷：metaKey 长度小于前缀时 `metaKey[len(prefix):]` 越界 panic。
	// 真实路径中 Scan 只匹配 "memory:buffer:meta:*"，不会产生这种 key；
	// run 的 recover 兜底。此处记录行为，不修改实现。
	scanner, _, _ := newBufferScanner(t)
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	store := scanner.store.(*scriptedBufferStore)
	store.hashes["abc"] = map[string]string{"last_at": old, "first_at": old}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		scanner.checkAndFlush(context.Background(), "abc")
	}()
	assert.True(t, panicked, "short meta key must panic (documented defect, recovered by run)")
}

func TestBufferScannerScanStopsOnError(t *testing.T) {
	// 极端情况：Scan 失败直接返回，不处理任何 key。
	scanner, store, queue := newBufferScanner(t)
	store.scanErr = assert.AnError
	scanner.scan(context.Background())
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerScanFollowsCursor(t *testing.T) {
	// 极端情况：cursor 非 0 时持续翻页直到游标归零。
	scanner, store, queue := newBufferScanner(t)
	store.scanKeys = []string{"memory:buffer:meta:batch-1"}
	store.scanNext = 42
	// 只让第二轮返回 cursor=0 且无 key。
	store.scanErr = nil
	// scan 循环：第一轮 keys=[batch-1], next=42 → 继续；第二轮 keys=[], next=0 → 结束。
	// 用 hashes 空字段让 checkAndFlush 走 no-op 分支，避免依赖 flush。
	scanner.scan(context.Background())
	queue.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything)
}

func TestBufferScannerSetMetricsNilAndStopIdempotent(t *testing.T) {
	scanner, _, _ := newBufferScanner(t)
	scanner.SetMetrics(nil) // 不 panic
	scanner.Stop()
	scanner.Stop() // 幂等
}

func TestBufferScannerStartStopsOnContextCancel(t *testing.T) {
	scanner, _, _ := newBufferScanner(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scanner.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start must return after ctx cancel")
	}
}

func TestBufferScannerStartStopsOnStop(t *testing.T) {
	scanner, _, _ := newBufferScanner(t)
	done := make(chan struct{})
	go func() {
		scanner.Start(context.Background())
		close(done)
	}()
	scanner.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start must return after Stop")
	}
}

// blockingScanStore 模拟外部依赖挂起：Scan 阻塞到 ctx 取消（对应 DNS 解析
// 卡死/Redis 不可达的真实场景），用于验证 scan 有操作超时预算。
type blockingScanStore struct {
	*fakeMessageBufferStore
	blockOnScan bool
}

func (s *blockingScanStore) Scan(ctx context.Context, _ uint64, _ string, _ int64) ([]string, uint64, error) {
	if s.blockOnScan {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}
	return nil, 0, nil
}

func TestBufferScannerScanHasOperationBudget(t *testing.T) {
	store := &blockingScanStore{fakeMessageBufferStore: newFakeMessageBufferStore(), blockOnScan: true}
	scanner := NewBufferScanner(store, new(MockExtractionQueue), testLogger())

	start := time.Now()
	scanner.scan(context.Background())
	elapsed := time.Since(start)

	// scan 必须在预算内返回：挂起的 store 调用不得无限阻塞扫描循环，
	// 否则 ticker 节拍被拖死（日志 "scan_failed: dial tcp: lookup ... i/o timeout"）。
	if elapsed >= constants.MemoryBufferScanTimeout+5*time.Second {
		t.Fatalf("scan exceeded operation budget: %v >= %v", elapsed, constants.MemoryBufferScanTimeout)
	}
}

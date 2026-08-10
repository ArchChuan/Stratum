package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestPollTenantQuarantinesMalformedPayloadBeforeDelete(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, logger: zap.NewNop(), batch: 10}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(7), []byte(`{"broken"`)))
	pool.ExpectExec("INSERT INTO memory_outbox_quarantine").
		WithArgs(int64(7), pgxmock.AnyArg(), "invalid_json").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("DELETE FROM memory_outbox").
		WithArgs([]int64{7}).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()

	if err := poller.pollTenant(context.Background(), "tenant_valid"); err != nil {
		t.Fatal(err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPollTenantKeepsMalformedPayloadWhenQuarantineFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, logger: zap.NewNop(), batch: 10}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(8), []byte(`{"broken"`)))
	pool.ExpectExec("INSERT INTO memory_outbox_quarantine").
		WithArgs(int64(8), pgxmock.AnyArg(), "invalid_json").
		WillReturnError(errors.New("quarantine unavailable"))
	pool.ExpectRollback()

	if err := poller.pollTenant(context.Background(), "tenant_valid"); err == nil {
		t.Fatal("expected quarantine failure")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func validOutboxPayload() []byte {
	return []byte(`{"message_id":"m1","tenant_id":"t1","user_id":"u1",` +
		`"agent_id":"a1","conversation_id":"c1","role":"user",` +
		`"content":"hello world","created_at":"2026-01-01T00:00:00Z"}`)
}

func TestPollTenantPublishFailureAfterCommitKeepsRowForRetry(t *testing.T) {
	// 回归：publish 曾在取出行事务内执行，失败会回滚已提交的取出；
	// 现在 publish 在事务提交后执行，失败必须保留行供下轮重试。
	_, nc := startJetStreamServer(t)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	ctx := context.Background()
	// stream 只接受不匹配的 subject，保证 publish 即时失败。
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     constants.MemoryRawStream,
		Subjects: []string{"unrelated.>"},
	})
	require.NoError(t, err)

	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, js: js, logger: zap.NewNop(), batch: 10}

	// 阶段1 事务必须已提交（ExpectCommit 不匹配即测试失败）。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(2), validOutboxPayload()))
	pool.ExpectCommit()
	// 无任何 DELETE 期望：失败行必须保留。

	err = poller.pollTenant(ctx, "tenant_valid")
	require.Error(t, err, "publish failure must be exposed, not swallowed")
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestPollTenantPublishSuccessThenConfirmDelivered(t *testing.T) {
	_, nc := startJetStreamServer(t)
	jsm, err := NewJetStreamManager(nc, zap.NewNop())
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, jsm.EnsureStreams(ctx))
	js := jsm.JS()

	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, js: js, logger: zap.NewNop(), batch: 10}

	// 阶段1：取出并提交（无网络 IO 在事务内）。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(1), validOutboxPayload()))
	pool.ExpectCommit()
	// 阶段3：投递成功后独立事务确认删除。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("DELETE FROM memory_outbox").
		WithArgs([]int64{1}).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()

	require.NoError(t, poller.pollTenant(ctx, "tenant_valid"))
	require.NoError(t, pool.ExpectationsWereMet())

	// 消息确已投递到 MEMORY_RAW stream。
	stream, err := js.Stream(ctx, constants.MemoryRawStream)
	require.NoError(t, err)
	info, err := stream.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.State.Msgs)
}

func TestOutboxPollerDynamicConfigOverridesStatic(t *testing.T) {
	p := &OutboxPoller{interval: time.Second, batch: 10}
	var dyn atomic.Pointer[DynamicConfig]
	dyn.Store(&DynamicConfig{PollInterval: 2 * time.Second, BatchSize: 20})
	p.WithDynamic(&dyn)

	if got := p.currentInterval(); got != 2*time.Second {
		t.Fatalf("currentInterval() = %v, want 2s", got)
	}
	if got := p.currentBatch(); got != 20 {
		t.Fatalf("currentBatch() = %d, want 20", got)
	}
}

func TestOutboxPollerDynamicConfigZeroValueFallsBackToStatic(t *testing.T) {
	p := &OutboxPoller{interval: time.Second, batch: 10}
	var dyn atomic.Pointer[DynamicConfig]
	p.WithDynamic(&dyn) // 指针非 nil，但未 Store 过

	if got := p.currentInterval(); got != time.Second {
		t.Fatalf("currentInterval() = %v, want static 1s", got)
	}
	if got := p.currentBatch(); got != 10 {
		t.Fatalf("currentBatch() = %d, want static 10", got)
	}
}

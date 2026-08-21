package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	vector "github.com/byteBuilderX/stratum/pkg/vector"
)

func TestKeepActiveFactResults_DropsNonActive(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("FROM memory_facts").
		WithArgs([]string{"f1", "f2"}).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f2"))
	pool.ExpectRollback()

	h := &RecallHandler{pool: pool, logger: zap.NewNop()}
	out, err := h.keepActiveFactResults(context.Background(), "t1",
		[]vector.SearchResult{{ID: "f1", Content: "stale"}, {ID: "f2", Content: "active"}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "f2", out[0].ID)
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestKeepLiveEntryResults_DropsExpired(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("FROM memory_entries").
		WithArgs([]string{"e1", "e2"}).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e2"))
	pool.ExpectRollback()

	h := &RecallHandler{pool: pool, logger: zap.NewNop()}
	out, err := h.keepLiveEntryResults(context.Background(), "t1",
		[]vector.SearchResult{{ID: "e1", Content: "expired"}, {ID: "e2", Content: "live"}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "e2", out[0].ID)
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestIntersectResults_FailClosedOnQueryError(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("FROM memory_facts").
		WithArgs([]string{"f1"}).
		WillReturnError(pgx.ErrTxClosed)
	pool.ExpectRollback()

	h := &RecallHandler{pool: pool, logger: zap.NewNop()}
	out, err := h.keepActiveFactResults(context.Background(), "t1", []vector.SearchResult{{ID: "f1"}})
	require.Error(t, err)
	require.Nil(t, out)
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestIntersectResults_NilPoolReturnsUnchanged(t *testing.T) {
	h := &RecallHandler{pool: nil, logger: zap.NewNop()}
	results := []vector.SearchResult{{ID: "f1"}}
	out, err := h.keepActiveFactResults(context.Background(), "t1", results)
	require.NoError(t, err)
	require.Equal(t, results, out)
}

type stubEntryVectorDeleter struct {
	calls [][]string
	err   error
}

func (s *stubEntryVectorDeleter) DeleteEntryVectors(_ context.Context, _ string, ids []string) error {
	if s.err != nil {
		return s.err
	}
	s.calls = append(s.calls, ids)
	return nil
}

func TestDeleteOrphanEntryVector(t *testing.T) {
	d := &stubEntryVectorDeleter{}
	deleteOrphanEntryVector(context.Background(), d, zap.NewNop(), "t1", "m1")
	require.Equal(t, [][]string{{"m1"}}, d.calls)
}

func TestDeleteOrphanEntryVector_NilCleanerOrEmptyIDNoOp(t *testing.T) {
	d := &stubEntryVectorDeleter{}
	deleteOrphanEntryVector(context.Background(), nil, zap.NewNop(), "t1", "m1")
	deleteOrphanEntryVector(context.Background(), d, zap.NewNop(), "t1", "")
	require.Empty(t, d.calls)
}

func TestEnricher_DeadLetterDeletesOrphanVector(t *testing.T) {
	ev := &MemoryEnrichedEvent{MemoryRawEvent: MemoryRawEvent{
		MessageID: "m1", TenantID: "tenant-a", UserID: "u1", Role: "user", Content: "hello",
	}}
	data, err := ev.Marshal()
	require.NoError(t, err)
	msg := &fakeJetStreamMsg{
		subject: "memory.enriched.tenant-a",
		data:    data,
		metadata: &jetstream.MsgMetadata{
			NumDelivered: 1,
			Sequence:     jetstream.SequencePair{Stream: 7},
		},
	}
	pub := &fakeDLQPublisher{}
	cleaner := &stubEntryVectorDeleter{}
	w := NewEnricherWorker(nil, pub, nil, zap.NewNop(), Config{EnrichAckWait: time.Second, MaxDeliver: 3})
	w.WithEntryVectorDeleter(cleaner)

	w.processMessage(context.Background(), msg)

	require.Equal(t, [][]string{{"m1"}}, cleaner.calls, "enrich 永久失败后必须删除孤儿向量")
	require.Equal(t, 1, msg.termCount, "llm 不可用必须 dead-letter")
}

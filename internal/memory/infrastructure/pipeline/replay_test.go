package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// seedDLQEvent 向 DLQ stream 写入一条死信事件。MsgID 与生产死信路径一致
// （deadLetterDedupID），保证重放标记的 dedup 覆盖语义在测试中同样生效。
func seedDLQEvent(t *testing.T, js jetstream.JetStream, ev DeadLetterEvent, headers nats.Header) {
	t.Helper()
	data, err := json.Marshal(ev)
	require.NoError(t, err)
	subject := fmt.Sprintf("%s.%s", constants.MemoryDLQSubject, ev.TenantID)
	_, err = js.PublishMsg(context.Background(), &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  headers,
	}, jetstream.WithMsgID(deadLetterDedupID(ev)))
	require.NoError(t, err)
}

type rawMessage struct {
	subject string
	data    []byte
}

// fetchAllRaw 读取 MEMORY_RAW 全部消息（每子测试只建一个消费者，
// 规避 WorkQueue stream 单活跃消费者的路由歧义）。
func fetchAllRaw(t *testing.T, js jetstream.JetStream) []rawMessage {
	t.Helper()
	// WorkQueue stream 的 pull 消费者必须显式 ack（err 10084）；
	// workqueue 上 ack 即删除消息，单次盘点后无需保留。
	consumer, err := js.CreateOrUpdateConsumer(context.Background(), constants.MemoryRawStream, jetstream.ConsumerConfig{
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: constants.MemoryRawSubject + ".>",
	})
	require.NoError(t, err)
	var got []rawMessage
	for {
		batch, err := consumer.Fetch(64, jetstream.FetchMaxWait(300*time.Millisecond))
		require.NoError(t, err)
		received := 0
		for msg := range batch.Messages() {
			received++
			got = append(got, rawMessage{subject: msg.Subject(), data: append([]byte(nil), msg.Data()...)})
			require.NoError(t, msg.Ack())
		}
		if received == 0 {
			return got
		}
	}
}

func rawCounts(got []rawMessage) map[string]int {
	counts := map[string]int{}
	for _, m := range got {
		counts[m.subject]++
	}
	return counts
}

type dlqMessage struct {
	header nats.Header
	data   []byte
}

// fetchAllDLQ 读取 MEMORY_DLQ 全部消息（含重放标记产生的副本），
// 用于断言 ReplayCount 是否真实推进。
func fetchAllDLQ(t *testing.T, js jetstream.JetStream) []dlqMessage {
	t.Helper()
	consumer, err := js.CreateOrUpdateConsumer(context.Background(), constants.MemoryDLQStream, jetstream.ConsumerConfig{
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: constants.MemoryDLQSubject + ".>",
	})
	require.NoError(t, err)
	var got []dlqMessage
	for {
		batch, err := consumer.Fetch(64, jetstream.FetchMaxWait(300*time.Millisecond))
		require.NoError(t, err)
		received := 0
		for msg := range batch.Messages() {
			received++
			got = append(got, dlqMessage{header: msg.Headers(), data: append([]byte(nil), msg.Data()...)})
			require.NoError(t, msg.Ack())
		}
		if received == 0 {
			return got
		}
	}
}

// TestReplayMsgIDs_ScopedByTenantStreamAndCount 锁定 MsgID 格式：raw 侧含租户
// 与原始流（跨租户/跨流同 seq 不碰撞），mark 侧含目标 count（窗口内可落地，
// 且与原消息 dlq: 前缀不同，首轮标记不被去重吞掉）。
func TestReplayMsgIDs_ScopedByTenantStreamAndCount(t *testing.T) {
	ev := DeadLetterEvent{TenantID: "tenant-a", OriginalStream: "MEMORY_RAW", StreamSequence: 42}
	same := DeadLetterEvent{TenantID: "tenant-a", OriginalStream: "MEMORY_RAW", StreamSequence: 42}
	otherTenant := DeadLetterEvent{TenantID: "tenant-b", OriginalStream: "MEMORY_RAW", StreamSequence: 42}
	otherStream := DeadLetterEvent{TenantID: "tenant-a", OriginalStream: "MEMORY_ENRICHED", StreamSequence: 42}

	if rawReplayMsgID(ev) != rawReplayMsgID(same) {
		t.Fatal("same event must produce a stable raw MsgID")
	}
	if rawReplayMsgID(ev) == rawReplayMsgID(otherTenant) {
		t.Fatal("raw MsgID must include the tenant")
	}
	if rawReplayMsgID(ev) == rawReplayMsgID(otherStream) {
		t.Fatal("raw MsgID must include the original stream")
	}
	if markReplayMsgID(ev, 1) == markReplayMsgID(ev, 2) {
		t.Fatal("mark MsgID must include the target count")
	}
	if markReplayMsgID(ev, 1) == rawReplayMsgID(ev) || markReplayMsgID(ev, 1) == deadLetterDedupID(ev) {
		t.Fatal("mark MsgID must be distinct from raw MsgID and the original dead-letter dedupID")
	}
}

// TestReplayService 覆盖定向重放的四个行为契约：
// G4 error_code + payload 双过滤；G6 幂等（重复调用不产生新消息）；
// G5 租户从事件派生（请求体无租户参数）；重放次数上限。
func TestReplayService(t *testing.T) {
	newSvc := func(t *testing.T) (*ReplayService, jetstream.JetStream) {
		t.Helper()
		_, nc := startJetStreamServer(t)
		jsm, err := NewJetStreamManager(nc, zaptest.NewLogger(t))
		require.NoError(t, err)
		require.NoError(t, jsm.EnsureStreams(context.Background()))
		svc, err := NewReplayService(nc, zaptest.NewLogger(t))
		require.NoError(t, err)
		return svc, jsm.JS()
	}
	event := func(tenant string, seq uint64, errorCode string, payload []byte) DeadLetterEvent {
		return DeadLetterEvent{
			TenantID:       tenant,
			Stage:          "embed",
			ErrorCode:      errorCode,
			OriginalStream: constants.MemoryRawStream,
			OriginalSubj:   constants.MemoryRawSubject + "." + tenant,
			StreamSequence: seq,
			FailedAt:       time.Now().UTC(),
			Payload:        payload,
		}
	}

	t.Run("replays only matching error_code with payload", func(t *testing.T) {
		svc, js := newSvc(t)
		payloadA := []byte(`{"content":"hi-a"}`)
		seedDLQEvent(t, js, event("tenant-a", 101, "embed_service_unavailable", payloadA), nil)
		// error_code 匹配但 payload 为空：跳过。
		seedDLQEvent(t, js, event("tenant-a", 102, "embed_service_unavailable", nil), nil)
		// payload 非空但 error_code 不匹配：跳过。
		seedDLQEvent(t, js, event("tenant-b", 103, "embedding_failed", []byte(`{"content":"hi-b"}`)), nil)

		result, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, ReplayResult{Total: 2, Replayed: 1, Skipped: 1}, result)

		raw := fetchAllRaw(t, js)
		counts := rawCounts(raw)
		assert.Equal(t, 1, counts[constants.MemoryRawSubject+".tenant-a"])
		assert.Zero(t, counts[constants.MemoryRawSubject+".tenant-b"])
		// 重放到 raw 的 body 必须与 DLQ 保存的原始 payload 一致。
		for _, m := range raw {
			assert.Equal(t, payloadA, m.data)
		}
	})

	t.Run("idempotent: second replay re-feeds nothing and does not over-report", func(t *testing.T) {
		svc, js := newSvc(t)
		seedDLQEvent(t, js, event("tenant-a", 104, "embed_service_unavailable", []byte(`{"content":"hi-a"}`)), nil)

		first, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, ReplayResult{Total: 1, Replayed: 1}, first)

		// 窗口内第二次重放：raw publish 复用同一 MsgID 被服务器去重（无错误），
		// 不得谎报 Replayed——按 skipped 语义计入。幂等契约（raw 条数不增长）不变；
		// 但 mark（含目标 count）照常发布，ReplayCount 继续推进。
		second, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Zero(t, second.Replayed)
		assert.Equal(t, 2, second.Skipped) // 原消息 + 首轮 mark 消息，raw 均被去重

		assert.Equal(t, 1, rawCounts(fetchAllRaw(t, js))[constants.MemoryRawSubject+".tenant-a"])
	})

	t.Run("replay count advances to cap within dedup window", func(t *testing.T) {
		svc, js := newSvc(t)
		seedDLQEvent(t, js, event("tenant-a", 108, "embed_service_unavailable", []byte(`{"content":"hi-a"}`)), nil)

		results := make([]ReplayResult, 0, MaxDLQReplay+1)
		for i := 0; i <= MaxDLQReplay; i++ {
			res, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
			require.NoError(t, err)
			results = append(results, res)
		}

		// 仅首轮真实 re-feed（raw MsgID 窗口内去重）；后续轮次不得谎报 Replayed。
		assert.Equal(t, 1, results[0].Replayed)
		for i := 1; i < len(results); i++ {
			assert.Zero(t, results[i].Replayed, "replay %d must not over-report replayed", i)
		}
		assert.Equal(t, 1, rawCounts(fetchAllRaw(t, js))[constants.MemoryRawSubject+".tenant-a"])

		// 直接证据：mark 含目标 count，窗口内 ReplayCount 推进到 MaxDLQReplay 并封顶。
		maxCount := 0
		for _, m := range fetchAllDLQ(t, js) {
			if n := replayCountOf(m.header); n > maxCount {
				maxCount = n
			}
		}
		assert.Equal(t, MaxDLQReplay, maxCount)

		// 封顶后整轮全跳过：count=3 的 mark 命中上限，其余消息 raw 去重。
		last := results[len(results)-1]
		assert.Equal(t, len(results), last.Skipped)
		assert.Zero(t, last.Replayed)
	})

	t.Run("same sequence from different streams and tenants does not collide", func(t *testing.T) {
		svc, js := newSvc(t)
		payloadA := []byte(`{"content":"hi-a"}`)
		payloadB := []byte(`{"content":"hi-b"}`)
		evA := event("tenant-a", 200, "embed_service_unavailable", payloadA)
		evA.OriginalStream = constants.MemoryRawStream
		evB := event("tenant-b", 200, "embed_service_unavailable", payloadB)
		evB.OriginalStream = constants.MemoryEnrichedStream
		seedDLQEvent(t, js, evA, nil)
		seedDLQEvent(t, js, evB, nil)

		result, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, ReplayResult{Total: 2, Replayed: 2}, result)

		counts := rawCounts(fetchAllRaw(t, js))
		// 旧实现 MsgID 仅 replay:<seq>：不同流/租户同 seq 时窗口内第二条被
		// 静默丢弃。修复后 MsgID 含租户与原始流，两条都必须真实落地。
		assert.Equal(t, 1, counts[constants.MemoryRawSubject+".tenant-a"])
		assert.Equal(t, 1, counts[constants.MemoryRawSubject+".tenant-b"])
	})

	t.Run("tenant derived from event not body", func(t *testing.T) {
		svc, js := newSvc(t)
		seedDLQEvent(t, js, event("tenant-a", 105, "embed_service_unavailable", []byte(`{"content":"hi-a"}`)), nil)

		// ReplayByErrorCode 不接受租户参数：重放目标只可能来自事件 TenantID，
		// 请求方无法指定其他租户，防止跨租户重放。
		result, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, 1, result.Replayed)

		counts := rawCounts(fetchAllRaw(t, js))
		assert.Equal(t, 1, counts[constants.MemoryRawSubject+".tenant-a"])
		assert.Zero(t, counts[constants.MemoryRawSubject+".tenant-b"])
	})

	t.Run("replay count cap rejects beyond max", func(t *testing.T) {
		svc, js := newSvc(t)
		seedDLQEvent(t, js, event("tenant-a", 106, "embed_service_unavailable", []byte(`{"content":"hi-a"}`)),
			nats.Header{replayHeaderReplayCount: {strconv.Itoa(MaxDLQReplay)}})

		result, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, 1, result.Skipped)
		assert.Zero(t, result.Replayed)
		assert.Zero(t, rawCounts(fetchAllRaw(t, js))[constants.MemoryRawSubject+".tenant-a"])
	})
}

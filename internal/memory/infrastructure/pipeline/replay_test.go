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

	t.Run("idempotent: second replay produces no new raw messages", func(t *testing.T) {
		svc, js := newSvc(t)
		seedDLQEvent(t, js, event("tenant-a", 104, "embed_service_unavailable", []byte(`{"content":"hi-a"}`)), nil)

		first, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, 1, first.Replayed)

		second, err := svc.ReplayByErrorCode(context.Background(), "embed_service_unavailable")
		require.NoError(t, err)
		assert.Equal(t, 1, second.Replayed)

		// 幂等契约：raw publish 复用 replay:<StreamSequence> MsgID，
		// JetStream dedup 窗口内重复调用被去重，raw 条数不随重复调用增长。
		assert.Equal(t, 1, rawCounts(fetchAllRaw(t, js))[constants.MemoryRawSubject+".tenant-a"])
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

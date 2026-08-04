package hermes

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

var errTest = errors.New("handler failed")

// metricsSpy 记录 Hermes 指标调用，嵌入 NoopMetrics 以满足 MetricsProvider。
type metricsSpy struct {
	observability.NoopMetrics
	mu     sync.Mutex
	events map[string]int
	ok     map[string]int
	errs   map[string]int
}

func newMetricsSpy() *metricsSpy {
	return &metricsSpy{events: map[string]int{}, ok: map[string]int{}, errs: map[string]int{}}
}

func (s *metricsSpy) IncHermesEvent(eventType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[eventType]++
}

func (s *metricsSpy) IncHermesEventProcessed(eventType, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == "ok" {
		s.ok[eventType]++
	} else {
		s.errs[eventType+":"+status]++
	}
}

func (s *metricsSpy) count(m map[string]int, key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return m[key]
}

func newNatsTestServer(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{
		Host:   "127.0.0.1",
		Port:   -1, // 随机端口
		NoLog:  true,
		NoSigs: true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.Eventually(t, func() bool { return ns.ReadyForConnections(50 * time.Millisecond) },
		5*time.Second, 10*time.Millisecond, "nats server not ready")
	t.Cleanup(ns.Shutdown)
	return ns
}

func newHermesTestClient(t *testing.T) (*Client, *metricsSpy) {
	t.Helper()
	ns := newNatsTestServer(t)
	spy := newMetricsSpy()
	client, err := NewClient(ns.ClientURL(), zap.NewNop(), spy)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, spy
}

func TestHermes_PublishSubscribeRoundTrip(t *testing.T) {
	client, spy := newHermesTestClient(t)

	received := make(chan *Event, 1)
	require.NoError(t, client.Subscribe("user.created", func(event *Event) error {
		received <- event
		return nil
	}))

	event := &Event{Type: "user.created", Timestamp: 1234, Source: "iam", Data: map[string]any{"id": "u1"}}
	require.NoError(t, client.Publish(event))

	select {
	case got := <-received:
		require.Equal(t, "user.created", got.Type)
		require.Equal(t, int64(1234), got.Timestamp)
		require.Equal(t, "iam", got.Source)
	case <-time.After(3 * time.Second):
		t.Fatal("event not delivered")
	}
	require.Eventually(t, func() bool { return spy.count(spy.events, "user.created") == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return spy.count(spy.ok, "user.created") == 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestHermes_SubscribeUnmarshalError(t *testing.T) {
	client, spy := newHermesTestClient(t)

	require.NoError(t, client.Subscribe("bad.json", func(event *Event) error { return nil }))

	// 直接发布非法 JSON 载荷
	subject := "events.bad.json"
	require.NoError(t, client.conn.Publish(subject, []byte("{not json")))

	require.Eventually(t, func() bool { return spy.count(spy.errs, "bad.json:unmarshal_error") == 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestHermes_SubscribeHandlerError(t *testing.T) {
	client, spy := newHermesTestClient(t)

	require.NoError(t, client.Subscribe("job.run", func(event *Event) error {
		return errTest
	}))

	require.NoError(t, client.Publish(&Event{Type: "job.run", Data: nil}))

	require.Eventually(t, func() bool { return spy.count(spy.errs, "job.run:handler_error") == 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestHermes_MultipleHandlersForSameType(t *testing.T) {
	client, _ := newHermesTestClient(t)

	first := make(chan struct{}, 1)
	second := make(chan struct{}, 1)
	require.NoError(t, client.Subscribe("evt", func(event *Event) error { first <- struct{}{}; return nil }))
	require.NoError(t, client.Subscribe("evt", func(event *Event) error { second <- struct{}{}; return nil }))

	require.NoError(t, client.Publish(&Event{Type: "evt"}))

	require.Eventually(t, func() bool { return len(first) == 1 && len(second) == 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestHermes_NewClientConnectFailure(t *testing.T) {
	client, err := NewClient("nats://127.0.0.1:1", zap.NewNop(), observability.NoopMetrics{})
	require.Error(t, err)
	require.Nil(t, client)
}

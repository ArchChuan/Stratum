package hermes

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
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
	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	spy := newMetricsSpy()
	client, err := NewClient(nc, zap.NewNop(), spy)
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

func TestHermes_NewClientNilConn(t *testing.T) {
	client, err := NewClient(nil, zap.NewNop(), observability.NoopMetrics{})
	require.Error(t, err)
	require.Nil(t, client)
}

func TestHermes_HandlerPanicRecovered(t *testing.T) {
	client, spy := newHermesTestClient(t)

	panicHandler := make(chan struct{}, 4)
	require.NoError(t, client.Subscribe("risky.event", func(event *Event) error {
		panicHandler <- struct{}{}
		panic("handler blew up")
	}))

	healthy := make(chan *Event, 4)
	require.NoError(t, client.Subscribe("risky.event", func(event *Event) error {
		healthy <- event
		return nil
	}))

	// 连续两条消息：panic 的 handler 被 recover 后订阅仍然存活，
	// 且同类型的健康 handler 不受影响（每消息都收到）。
	require.NoError(t, client.Publish(&Event{Type: "risky.event", Data: map[string]any{"id": 1}}))
	require.NoError(t, client.Publish(&Event{Type: "risky.event", Data: map[string]any{"id": 2}}))

	require.Eventually(t, func() bool { return len(panicHandler) == 2 }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(healthy) == 2 }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return spy.count(spy.errs, "risky.event:handler_panic") == 2 },
		3*time.Second, 10*time.Millisecond)
	require.Equal(t, 2, spy.count(spy.ok, "risky.event"))
}

func TestHermes_QueueSubscriptionUsesQueueGroup(t *testing.T) {
	client, _ := newHermesTestClient(t)

	require.NoError(t, client.Subscribe("q.evt", func(*Event) error { return nil }))

	client.mu.RLock()
	sub := client.subscriptions["q.evt"]
	client.mu.RUnlock()
	require.NotNil(t, sub)
	require.Equal(t, "hermes.q.evt", sub.Queue)
}

func TestHermes_RepeatedSubscribeSharesSubscription(t *testing.T) {
	client, _ := newHermesTestClient(t)

	require.NoError(t, client.Subscribe("dup.evt", func(*Event) error { return nil }))
	require.NoError(t, client.Subscribe("dup.evt", func(*Event) error { return nil }))

	client.mu.RLock()
	defer client.mu.RUnlock()
	// 同类型只建一条 queue 订阅，handler 全部挂在同一条上
	require.NotNil(t, client.subscriptions["dup.evt"])
	require.Len(t, client.handlers["dup.evt"], 2)
}

func TestHermes_QueueGroupLoadBalances(t *testing.T) {
	ns := newNatsTestServer(t)

	ncA, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(ncA.Close)
	ncB, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(ncB.Close)

	clientA, err := NewClient(ncA, zap.NewNop(), newMetricsSpy())
	require.NoError(t, err)
	t.Cleanup(clientA.Close)
	clientB, err := NewClient(ncB, zap.NewNop(), newMetricsSpy())
	require.NoError(t, err)
	t.Cleanup(clientB.Close)

	var gotA, gotB atomic.Int32
	require.NoError(t, clientA.Subscribe("queue.evt", func(*Event) error { gotA.Add(1); return nil }))
	require.NoError(t, clientB.Subscribe("queue.evt", func(*Event) error { gotB.Add(1); return nil }))
	// 等待两条订阅在服务端注册完成，避免消息先于 SUB 到达而丢失
	require.NoError(t, ncA.Flush())
	require.NoError(t, ncB.Flush())

	const total = 10
	for i := 0; i < total; i++ {
		require.NoError(t, clientA.Publish(&Event{Type: "queue.evt"}))
	}

	// 队列组语义：每条消息只被一个成员消费（非广播），总量不丢不重
	require.Eventually(t, func() bool { return gotA.Load()+gotB.Load() == total },
		3*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(total), gotA.Load()+gotB.Load())
	// 负载均衡：两个成员都有消费份额
	require.GreaterOrEqual(t, gotA.Load(), int32(1))
	require.GreaterOrEqual(t, gotB.Load(), int32(1))
}

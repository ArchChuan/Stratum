package a2a

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDefaultProtocolConfig(t *testing.T) {
	cfg := DefaultProtocolConfig()
	if cfg.HeartbeatInterval != 30*time.Second ||
		cfg.TaskTimeout != 5*time.Minute ||
		cfg.MaxRetries != 3 ||
		cfg.RetryBackoff != time.Second ||
		cfg.PeerCleanupInterval != 5*time.Minute ||
		!cfg.EnableTracing {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestNewA2AProtocolNilConfig(t *testing.T) {
	// 极端情况：nil config 回退默认，不 panic。
	p := NewA2AProtocol(nil, zap.NewNop())
	if p.config == nil || p.config.HeartbeatInterval != 30*time.Second {
		t.Fatalf("config = %+v", p.config)
	}
	if p.metrics == nil || p.clients == nil {
		t.Fatal("metrics/clients must be initialized")
	}
}

func TestProtocolStartStopIdempotent(t *testing.T) {
	p := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 极端情况：未 Start 直接 Stop 是 no-op。
	if err := p.Stop(); err != nil {
		t.Fatalf("stop before start = %v", err)
	}

	if err := p.Start(ctx); err != nil {
		t.Fatalf("start = %v", err)
	}
	// 极端情况：重复 Start 幂等。
	if err := p.Start(ctx); err != nil {
		t.Fatalf("second start = %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("stop = %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("second stop = %v", err)
	}
}

func TestProtocolCreateAndGetClient(t *testing.T) {
	p := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())
	client := p.CreateClient("a1", "Agent a1")
	if client == nil || client.GetAgentID() != "a1" {
		t.Fatalf("client = %+v", client)
	}
	if got, ok := p.GetClient("a1"); !ok || got != client {
		t.Fatalf("GetClient = %+v, %v", got, ok)
	}
	// 极端情况：未注册的 agent。
	if _, ok := p.GetClient("ghost"); ok {
		t.Fatal("ghost client must not exist")
	}
}

func TestProtocolMetrics(t *testing.T) {
	p := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())

	m := p.GetMetrics()
	if m.MessagesSent != 0 || m.MessagesReceived != 0 || m.MessagesFailed != 0 {
		t.Fatalf("initial metrics = %+v", m)
	}
	p.IncrementMessageSent()
	p.IncrementMessageSent()
	p.IncrementMessageReceived()
	p.IncrementMessageFailed()
	m = p.GetMetrics()
	if m.MessagesSent != 2 || m.MessagesReceived != 1 || m.MessagesFailed != 1 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestProtocolSendHeartbeats(t *testing.T) {
	// 手动触发 sendHeartbeats（不依赖 ticker），验证 outbox 收到 heartbeat。
	p := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())
	if err := p.discovery.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}}); err != nil {
		t.Fatalf("register = %v", err)
	}

	// 极端情况：无 peer 时静默。
	p2 := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())
	p2.sendHeartbeats()

	p.sendHeartbeats()
	msg := readOutbox(p.handler)
	if msg == nil || msg.Type != MessageTypeHeartbeat || msg.To.ID != "a1" {
		t.Fatalf("heartbeat = %+v", msg)
	}
	if msg.From.ID != "system" || msg.From.Name != "a2a-protocol" {
		t.Fatalf("heartbeat from = %+v", msg.From)
	}
}

func TestProtocolRunLoopsExitOnCancel(t *testing.T) {
	// 极端情况：ctx 取消后心跳/清理循环退出，Stop 不 hang。
	cfg := DefaultProtocolConfig()
	cfg.HeartbeatInterval = time.Millisecond
	cfg.PeerCleanupInterval = time.Millisecond
	p := NewA2AProtocol(cfg, zap.NewNop())
	_ = p.discovery.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})

	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start = %v", err)
	}
	time.Sleep(5 * time.Millisecond) // 让循环跑几拍
	cancel()
	if err := p.Stop(); err != nil {
		t.Fatalf("stop = %v", err)
	}
	// Stop 后循环必须退出：sendHeartbeats 不再产生消息。
	if msg := readOutbox(p.handler); msg != nil && msg.Type != MessageTypeHeartbeat {
		t.Fatalf("unexpected msg = %+v", msg)
	}
}

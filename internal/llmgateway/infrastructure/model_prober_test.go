package infrastructure_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// proberServer 按路径分派：/chat/completions 恒 500（探活失败）、/embeddings
// 返回 200（探活成功），并计数两类请求。
type proberServer struct {
	chatCalls  atomic.Int32
	embedCalls atomic.Int32
}

func newProberServer(t *testing.T) (*proberServer, *httptest.Server) {
	t.Helper()
	s := &proberServer{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			s.chatCalls.Add(1)
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
		case "/embeddings":
			s.embedCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

// newProbeProto 构造绑定探活 server 的 OpenAICompat 协议实例（同时实现
// ChatProtocol 与 EmbedProtocol，与 wiring 中共享协议实例的模式一致）。
func newProbeProto(ts *httptest.Server) *infrastructure.OpenAICompatProtocol {
	return infrastructure.NewOpenAICompatProtocol(infrastructure.NewOpenAICompatClient(
		infrastructure.ProviderConfig{Name: "p", BaseURL: ts.URL}, zap.NewNop()))
}

// TestModelProber_ProbeDrivesHealth 验证探活主动信号驱动健康状态并按能力
// 分派：chat 模型探活失败（/chat/completions 500）→ degraded；embedding 模型
// 探活成功（/embeddings 200）→ healthy。
func TestModelProber_ProbeDrivesHealth(t *testing.T) {
	server, ts := newProberServer(t)
	health := infrastructure.NewHealthRegistry(nil)
	proto := newProbeProto(ts)
	prober := infrastructure.NewModelProber(
		&mockModelRepo{models: []domain.Model{
			{Name: "chat-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
			{Name: "embed-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
		}},
		&mockProviderRepo{providers: map[string]*domain.Provider{
			"p1": {Name: "p", BaseURL: ts.URL, APIKey: "k", Enabled: true, Kind: domain.ProviderOpenAICompat},
		}},
		map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto},
		map[domain.ProviderKind]infrastructure.EmbedProtocol{domain.ProviderOpenAICompat: proto},
		health,
		zap.NewNop(),
	)

	prober.ProbeOnce(context.Background())

	require.GreaterOrEqual(t, server.chatCalls.Load(), int32(1))
	require.GreaterOrEqual(t, server.embedCalls.Load(), int32(1))
	require.Equal(t, infrastructure.ModelHealthDegraded, health.Get("chat-model").Status)
	require.Equal(t, infrastructure.ModelHealthHealthy, health.Get("embed-model").Status)
}

// TestModelProber_ProbeSkippedInsideRecoveryWindow 验证探活 worker 尊重
// HealthRegistry 状态机：unhealthy 模型在 recovery 窗口内 AllowProbe 返回
// false，探活 worker 跳过不请求；窗口过后放行真正探测。
func TestModelProber_ProbeSkippedInsideRecoveryWindow(t *testing.T) {
	server, ts := newProberServer(t)
	clock := &proberClock{cur: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}
	health := infrastructure.NewHealthRegistry(clock.Now)
	proto := newProbeProto(ts)
	prober := infrastructure.NewModelProber(
		&mockModelRepo{models: []domain.Model{
			{Name: "chat-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		}},
		&mockProviderRepo{providers: map[string]*domain.Provider{
			"p1": {Name: "p", BaseURL: ts.URL, APIKey: "k", Enabled: true, Kind: domain.ProviderOpenAICompat},
		}},
		map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto},
		nil,
		health,
		zap.NewNop(),
	)

	// 驱动 chat-model 到 unhealthy：5 次失败 → degraded，超 recovery 窗口再失败 → unhealthy
	for i := 0; i < 5; i++ {
		health.RecordFailure("chat-model", fmt.Errorf("upstream %d", i))
	}
	clock.Advance(40 * time.Second)
	health.RecordFailure("chat-model", fmt.Errorf("still down"))
	require.Equal(t, infrastructure.ModelHealthUnhealthy, health.Get("chat-model").Status)

	// 窗口内（lastFailure 距今 10s < recovery 30s）：AllowProbe=false，不请求
	clock.Advance(10 * time.Second)
	before := server.chatCalls.Load()
	prober.ProbeOnce(context.Background())
	require.Equal(t, before, server.chatCalls.Load())

	// 超窗口后（累计 35s > recovery）：放行探测；/chat/completions 恒 500 →
	// halfOpen 探测失败回 unhealthy
	clock.Advance(25 * time.Second)
	prober.ProbeOnce(context.Background())
	require.Greater(t, server.chatCalls.Load(), before)
	require.Equal(t, infrastructure.ModelHealthUnhealthy, health.Get("chat-model").Status)
}

// TestModelProber_StartStopLifecycle 验证 Start/Stop 生命周期：Start 首轮探活、
// Stop 关闭并等待 goroutine 退出（无泄漏）。
func TestModelProber_StartStopLifecycle(t *testing.T) {
	server, ts := newProberServer(t)
	health := infrastructure.NewHealthRegistry(nil)
	proto := newProbeProto(ts)
	prober := infrastructure.NewModelProber(
		&mockModelRepo{models: []domain.Model{
			{Name: "chat-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		}},
		&mockProviderRepo{providers: map[string]*domain.Provider{
			"p1": {Name: "p", BaseURL: ts.URL, APIKey: "k", Enabled: true, Kind: domain.ProviderOpenAICompat},
		}},
		map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto},
		nil,
		health,
		zap.NewNop(),
	).WithInterval(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	prober.Start(ctx)
	// 等待至少一轮探活完成（interval 5ms，轮询直至收到请求）
	deadline := time.Now().Add(2 * time.Second)
	for server.chatCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.GreaterOrEqual(t, server.chatCalls.Load(), int32(1))
	cancel()
	prober.Stop()
}

// proberClock 是可推进时钟（HealthRegistry 时间注入，recovery 窗口控制）。
type proberClock struct {
	mu  sync.Mutex
	cur time.Time
}

func (c *proberClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *proberClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
}

package wiring

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// closedAddr 返回一个刚释放的本地端口：此刻无人监听，连接被立刻拒绝，用来表达
// 「Redis 不可达」而不依赖网络超时。
func closedAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

// TestConnectRedisDegradesWhenRedisUnreachable 钉住启动策略：Redis 不可达只 WARN
// 降级，不阻断容器构建。否则一次 Redis 抖动会让整个服务起不来（CrashLoopBackOff），
// 而限流本可以在请求路径上 fail closed。
func TestConnectRedisDegradesWhenRedisUnreachable(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	target := "redis://" + closedAddr(t) + "?dial_timeout=200ms&max_retries=-1"
	client, err := connectRedis(context.Background(), target, zap.New(core))
	if err != nil {
		t.Fatalf("connectRedis on unreachable Redis = %v, want degraded start", err)
	}
	if client == nil {
		t.Fatal("connectRedis returned nil; rate limiting must keep failing closed on a real client")
	}
	t.Cleanup(func() { _ = client.Close() })

	if got := logs.FilterMessage("redis unavailable at startup, starting degraded").Len(); got != 1 {
		t.Fatalf("degraded start logged %d times, want 1: %v", got, logs.All())
	}
}

// TestConnectRedisNeverLogsCredentials 钉住降级日志只带 host:port：URL 可能内含密码，
// 整体打入日志就是凭据泄漏。
func TestConnectRedisNeverLogsCredentials(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	if _, err := connectRedis(context.Background(),
		"redis://stratumuser:sup3rsecret@"+closedAddr(t)+"?dial_timeout=200ms&max_retries=-1",
		zap.New(core)); err != nil {
		t.Fatalf("connectRedis: %v", err)
	}

	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "sup3rsecret") {
			t.Fatalf("log message leaked credentials: %q", entry.Message)
		}
		for _, field := range entry.Context {
			if strings.Contains(field.String, "sup3rsecret") {
				t.Fatalf("log field %q leaked credentials", field.Key)
			}
		}
	}
}

// TestConnectRedisRejectsInvalidURL 钉住「配置错误仍然致命」：降级启动不能把写错的
// REDIS_URL 伪装成「Redis 暂时不可用」。
func TestConnectRedisRejectsInvalidURL(t *testing.T) {
	core, _ := observer.New(zapcore.WarnLevel)
	client, err := connectRedis(context.Background(), "not-a-url", zap.New(core))
	if err == nil {
		t.Fatal("connectRedis(invalid url) = nil error, want config error")
	}
	if client != nil {
		t.Fatal("connectRedis(invalid url) returned a client alongside an error")
	}
}

// TestConnectRedisReportsConnectedWhenReachable 防止降级路径被误扩展到健康路径：
// Redis 可达时必须记录 connected 且不产生降级告警。
func TestConnectRedisReportsConnectedWhenReachable(t *testing.T) {
	mini := miniredis.RunT(t)
	core, logs := observer.New(zapcore.InfoLevel)

	client, err := connectRedis(context.Background(), "redis://"+mini.Addr(), zap.New(core))
	if err != nil {
		t.Fatalf("connectRedis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if got := logs.FilterMessage("redis connected").Len(); got != 1 {
		t.Fatalf("connected log count = %d, want 1: %v", got, logs.All())
	}
	if got := logs.FilterMessage("redis unavailable at startup, starting degraded").Len(); got != 0 {
		t.Fatalf("reachable Redis still logged a degraded start: %v", logs.All())
	}
}

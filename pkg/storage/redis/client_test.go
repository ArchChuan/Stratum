package redis_test

import (
	"context"
	"net"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// newUnconnectedClient 返回一个从不拨号的 go-redis 客户端：go-redis 的连接是
// 惰性的，Close 在从未连接时同样安全（只关连接池）。用它才能在不依赖真实 Redis
// 的前提下走到 Client.Close() 的第一行——那正是 nil logger 的 panic 点。
func newUnconnectedClient() *goredis.Client {
	return goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
}

// TestWrappedClientCloseWithNilLoggerDoesNotPanic 钉住 Wrap 的构造点归一化。
// Wrap 的前身只设 client 字段，Close 的第一行 c.logger.Info 会在 nil *zap.Logger
// 上直接 panic，且 panic 发生在 c.client.Close() 之前，底层连接一并泄漏。
func TestWrappedClientCloseWithNilLoggerDoesNotPanic(t *testing.T) {
	c := redis.Wrap(newUnconnectedClient())
	if err := c.Close(); err != nil {
		t.Fatalf("Close on a wrapped client: %v", err)
	}
}

// TestDuplicateCarriesNonNilLogger 钉住 Duplicate 的 logger 传播：wiring 把
// Duplicate 产出的实例注册进 Container 关闭链，若 logger 为 nil，关闭顺序测试
// 之外的任何真实 Shutdown 都会 panic。
func TestDuplicateCarriesNonNilLogger(t *testing.T) {
	dup := redis.Wrap(newUnconnectedClient()).Duplicate()
	if err := dup.Close(); err != nil {
		t.Fatalf("Close on a duplicated client: %v", err)
	}
}

// closedAddr 返回一个刚释放的本地端口：此刻无人监听，连接被立刻拒绝。用它代替
// 真实 Redis 表达「不可达」，不依赖网络超时，也不会误伤同机其它服务。
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

// TestNewDoesNotVerifyConnectivity 钉住 New 的契约：只有配置错误才是错误，Redis
// 不可达不返回错误。wiring 的「降级启动」依赖这一点——否则 Redis 抖动会让进程
// 无法启动，只剩 CrashLoopBackOff。
func TestNewDoesNotVerifyConnectivity(t *testing.T) {
	client, err := redis.New(
		"redis://"+closedAddr(t)+"?dial_timeout=200ms&max_retries=-1",
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("New on unreachable Redis = %v, want a usable client", err)
	}
	if client.Client() == nil {
		t.Fatal("New returned a wrapper without an underlying go-redis client")
	}

	// 连通性由调用方显式校验：这里必须报错，且失败后客户端依旧可用（可重拨）。
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping against unreachable Redis = nil, want connection error")
	}
	if client.Client().Options().Addr == "" {
		t.Fatal("client lost its address after a failed Ping")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close after failed Ping: %v", err)
	}
}

// TestNewRejectsInvalidURL 钉住另一半契约：URL 非法属于配置错误，必须致命，
// 否则降级启动会把「配置写错」伪装成「Redis 暂时不可用」。
func TestNewRejectsInvalidURL(t *testing.T) {
	client, err := redis.New("not-a-url", zap.NewNop())
	if err == nil {
		if client != nil {
			_ = client.Close()
		}
		t.Fatal("New(invalid url) = nil error, want config error")
	}
	if client != nil {
		t.Fatal("New(invalid url) returned a client alongside an error")
	}
}

// TestClientRecoversAfterRedisBecomesReachable 钉住「降级启动 + 后台重连」的闭环：
// 同一个客户端在 Redis 不可达时构造成功、Ping 失败，等 Redis 起来后无需重建实例
// 即可正常工作。真实拨号由连接池在命令里按需发起，这正是降级启动成立的前提。
func TestClientRecoversAfterRedisBecomesReachable(t *testing.T) {
	addr := closedAddr(t)
	client, err := redis.New("redis://"+addr+"?dial_timeout=200ms&max_retries=-1", zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping while Redis is down = nil, want error")
	}

	mini := miniredis.NewMiniRedis()
	if err := mini.StartAddr(addr); err != nil {
		t.Fatalf("start miniredis on %s: %v", addr, err)
	}
	t.Cleanup(mini.Close)

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("client did not reconnect after Redis returned: %v", err)
	}
	if got := client.Addr(); got != addr {
		t.Fatalf("Addr() = %q, want %q", got, addr)
	}
}

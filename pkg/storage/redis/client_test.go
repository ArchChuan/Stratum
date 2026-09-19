package redis_test

import (
	"testing"

	goredis "github.com/redis/go-redis/v9"

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

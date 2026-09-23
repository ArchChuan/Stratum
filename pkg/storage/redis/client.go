// Package redis provides Redis client wrappers and consumer-side KV interfaces.
// Business code depends on KVStore (in store.go), not on *goredis.Client directly.
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Client struct {
	client *goredis.Client
	logger *zap.Logger
}

// New 解析 URL 并构造客户端；只有配置错误（非法 URL）返回 error。
//
// 连通性刻意不在这里校验：go-redis 是惰性客户端，首个命令才拨号，失败后由连接池
// 在后续命令里按需重拨（baseClient.process 另有 MaxRetries 次重试）。所以「启动时
// Redis 不可达」不会让客户端永久失效，Redis 恢复后无需重启即可继续工作。
//
// 需要「启动即验证连通性」的调用方显式调用 Ping，并自行决定失败是致命还是降级
// （api/wiring/storage.go 选择降级启动 + 请求路径 fail closed）。
func New(url string, logger *zap.Logger) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}
	// go-redis 默认把读写 deadline 换成 context.Background()（baseClient.context），
	// 调用方的超时预算只在拨号阶段生效，命令本身会一直等到 ReadTimeout（默认 3s）×
	// MaxRetries 才返回。开启后调用方 ctx 的 deadline 才真正约束「含读写的整条命令」，
	// 这是限流 fail-closed 预算（constants.RateLimitRedisTimeout）成立的前提。
	// 订阅/阻塞读（PubSub、无 deadline 的 ctx）行为不变。
	opts.ContextTimeoutEnabled = true

	return &Client{client: goredis.NewClient(opts), logger: logger}, nil
}

func (c *Client) Client() *goredis.Client { return c.client }

// Addr 返回 host:port（不含凭据），用于日志；不要把整个 URL 写进日志。
func (c *Client) Addr() string { return c.client.Options().Addr }

// Ping 校验此刻的连通性。失败只代表当前不可达：客户端依旧可用，后续命令会重新
// 拨号，因此调用方可以降级而不是丢弃客户端。
func (c *Client) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}
	return nil
}

func (c *Client) Close() error {
	c.logger.Info("redis connection closed")
	return c.client.Close() //nolint:errcheck
}

// Wrap returns a Client wrapping an externally-owned *goredis.Client.
// Symmetric with postgres.Wrap; used to adopt connections owned by
// cmd/server/main.go without reconnecting.
//
// 注意：Wrap 只能包装，不能改写调用方 client 的 Options。依靠调用方 deadline 做
// fail-closed 预算的下游（如限流）只对经 New 构造的客户端成立。
//
// Wrap 无法拿到调用方的 logger（签名与 postgres.Wrap 对称），因此在构造点归一化为
// no-op：Close 的第一行就解引用 c.logger，留 nil 会让「注册进关闭链」的实例在
// 关闭时 panic（且 panic 发生在 c.client.Close() 之前，底层连接一并泄漏）。
// 归一化而不是在 Close 里判空：与 application 侧 deps.Logger == nil → zap.NewNop()
// 同型，保证「任何出口的 Client.logger 都非 nil」这一不变量只有一个维护点。
func Wrap(c *goredis.Client) *Client {
	return &Client{client: c, logger: zap.NewNop()}
}

// Duplicate 返回共享同一份连接参数、但拥有独立连接池的新 Client。
// go-redis 的 Pub/Sub 独占一条连接，控制通道必须与 Stream 命令分开实例，
// 否则订阅期间普通命令会在同一连接上排队饿死。
// 调用方负责关闭返回的实例。
func (c *Client) Duplicate() *Client {
	return &Client{client: goredis.NewClient(c.client.Options()), logger: c.logger}
}

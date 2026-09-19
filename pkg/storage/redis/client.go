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

func New(ctx context.Context, url string, logger *zap.Logger) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}

	client := goredis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	logger.Info("redis connected", zap.String("addr", opts.Addr))
	return &Client{client: client, logger: logger}, nil
}

func (c *Client) Client() *goredis.Client { return c.client }

func (c *Client) Close() error {
	c.logger.Info("redis connection closed")
	return c.client.Close() //nolint:errcheck
}

// Wrap returns a Client wrapping an externally-owned *goredis.Client.
// Symmetric with postgres.Wrap; used to adopt connections owned by
// cmd/server/main.go without reconnecting.
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

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
func Wrap(c *goredis.Client) *Client {
	return &Client{client: c}
}

// Duplicate 返回共享同一份连接参数、但拥有独立连接池的新 Client。
// go-redis 的 Pub/Sub 独占一条连接，控制通道必须与 Stream 命令分开实例，
// 否则订阅期间普通命令会在同一连接上排队饿死。
// 调用方负责关闭返回的实例。
func (c *Client) Duplicate() *Client {
	return &Client{client: goredis.NewClient(c.client.Options()), logger: c.logger}
}

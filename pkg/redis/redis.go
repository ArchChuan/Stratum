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

// Package redis re-exports pkg/storage/redis for backwards compatibility.
// New code should import pkg/storage/redis directly. Will be removed in phase 5.
package redis

import (
	"context"

	storageredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
	"go.uber.org/zap"
)

type Client = storageredis.Client

// New 保留旧契约：解析 + 建连校验，Redis 不可达即返回 error。
// 新的降级启动路径直接用 storage/redis：New（配置）+ Ping（连通性）。
func New(ctx context.Context, url string, logger *zap.Logger) (*storageredis.Client, error) {
	client, err := storageredis.New(url, logger)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx); err != nil {
		// 与旧实现一致：建连失败时关闭刚创建（未拨号）的连接池。
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

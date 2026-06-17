// Package redis re-exports pkg/storage/redis for backwards compatibility.
// New code should import pkg/storage/redis directly. Will be removed in phase 5.
package redis

import (
	"context"

	storageredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
	"go.uber.org/zap"
)

type Client = storageredis.Client

func New(ctx context.Context, url string, logger *zap.Logger) (*storageredis.Client, error) {
	return storageredis.New(ctx, url, logger)
}

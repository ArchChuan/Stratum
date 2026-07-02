// Package api is a thin compatibility shim during DDD refactor. It
// delegates router construction to api/http via wiring.NewFromExisting.
// Removed in Task 10c when cmd/server/main.go switches to the
// container-driven entrypoint directly.
package api

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	mempipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
)

// SetupRouter is the legacy entrypoint. cmd/server/main.go will move
// to wiring.BuildContainer + apihttp.NewRouter in Task 10c.
func SetupRouter(
	cfg *config.Config,
	logger *zap.Logger,
	gateway *llmgateway.Gateway,
	db *pgxpool.Pool,
	rdb *goredis.Client,
	skillAdapter port.Adapter,
	memPipeline *mempipeline.Pipeline,
) *gin.Engine {
	c, err := wiring.NewFromExisting(context.Background(), cfg, logger, gateway, db, rdb, skillAdapter, memPipeline)
	if err != nil {
		logger.Error("wiring.NewFromExisting failed; returning empty router", zap.Error(err))
		return gin.New()
	}
	return apihttp.NewRouter(c)
}

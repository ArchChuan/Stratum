// Command cleanup-builtin-knowledge-milvus 一次性删除内置知识库(已废弃)的共享
// Milvus collection。内置 workspace a0a0a0a0-0000-0000-0000-000000000001 跨租户固定、
// 向量无 tenantID 字段(VectorDocument 仅 ID/Content/SourceDocument/ChunkIndex/Vector),
// 无法按租户过滤,只能整体删除该 workspace 前缀下的全部 collection——legacy 名
// kb_<san(wsID)> 与各模型后缀变体 kb_<san(wsID)>_<model>(embed 模型历史未知,
// 故按前缀枚举,不硬编码任何模型名)。内置知识库整体废弃,全删安全;绝不触碰
// 其他前缀的 collection。
// 默认 dry-run 只列出匹配集合;显式 -execute 才 DropCollection(破坏性,须用户确认)。
// 与 scripts/cleanup-builtin-knowledge-workspace.sh(DB 侧)配套,发布后手动执行一次,
// 用完即弃,不留启动路径。
// 用法:MILVUS_HOST=... MILVUS_PORT=... go run ./cmd/cleanup-builtin-knowledge-milvus [-execute]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

const (
	cleanupTimeout = 5 * time.Minute
	// builtinWorkspaceID 是原 BuiltinWorkspaceID 常量(已随内置知识库移除),此处硬编码
	// 为清理锚点;内置 workspace ID 跨租户固定,不会与业务 workspace 冲突。
	builtinWorkspaceID = "a0a0a0a0-0000-0000-0000-000000000001"
)

// collectionDropper 缩小到清理所需的 Milvus 能力,便于测试注入 stub。
type collectionDropper interface {
	ListCollections(ctx context.Context, prefix string) ([]string, error)
	DeleteCollection(ctx context.Context, collectionName string) error
}

var _ collectionDropper = (*milvus.VectorStore)(nil)

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	if err := run(os.Args[1:], os.Getenv, logger); err != nil {
		logger.Error("cleanup builtin knowledge milvus failed", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
	_ = logger.Sync()
}

func run(args []string, getenv func(string) string, logger *zap.Logger) error {
	flags := flag.NewFlagSet("cleanup-builtin-knowledge-milvus", flag.ContinueOnError)
	execute := flags.Bool("execute", false, "真删匹配的 collection(默认 dry-run 只列出)")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	host, port := getenv("MILVUS_HOST"), getenv("MILVUS_PORT")
	if host == "" || port == "" {
		return errors.New("MILVUS_HOST and MILVUS_PORT are required")
	}

	vs := milvus.NewVectorStore(host, port, logger)
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := vs.Connect(ctx); err != nil {
		return fmt.Errorf("connect milvus: %w", err)
	}
	defer vs.Close()

	affected, err := cleanup(ctx, vs, *execute)
	if err != nil {
		return err
	}
	if len(affected) == 0 {
		logger.Info("no builtin-knowledge collections found")
		return nil
	}
	for _, c := range affected {
		if *execute {
			logger.Info("collection dropped", zap.String("collection", c))
		} else {
			logger.Info("would drop collection", zap.String("collection", c))
		}
	}
	logger.Info("cleanup done", zap.Int("collections", len(affected)), zap.Bool("dry_run", !*execute))
	return nil
}

// cleanup 枚举内置 workspace 前缀下的全部 collection(legacy 名与各模型后缀),
// dryRun 为 true 只列出、false 逐个 DropCollection。返回受影响(将删/已删)的
// collection 名;枚举失败与删除失败必须传播,不能静默漏删。collection 不存在
// (并发/已删)按无操作跳过,保持幂等。
func cleanup(ctx context.Context, vs collectionDropper, dryRun bool) ([]string, error) {
	legacy := constants.CollectionLegacyName("", builtinWorkspaceID)
	cols, err := vs.ListCollections(ctx, legacy)
	if err != nil {
		return nil, fmt.Errorf("list collections %s: %w", legacy, err)
	}
	affected := make([]string, 0, len(cols))
	for _, c := range cols {
		if !dryRun {
			if err := vs.DeleteCollection(ctx, c); err != nil && !errors.Is(err, milvus.ErrCollectionNotFound) {
				return nil, fmt.Errorf("delete collection %s: %w", c, err)
			}
		}
		affected = append(affected, c)
	}
	return affected, nil
}

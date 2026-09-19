package wiring

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	versioningpersistence "github.com/byteBuilderX/stratum/internal/versioning/infrastructure/persistence"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// wireAgentRepoDeps 装配 DB-backed 的 agent deps：资源编辑器、通用产品版本历史、
// created_by 昵称解析，以及断线续传的执行租约。
//
// 从 buildAgent 提取：该函数是存量超长函数（棘轮基线 145 行），新增字段不得内联。
//
// 租约必须复用 checkpoint 的**同一个** PgCheckpointStore 实例——checkpoint 与租约
// 同源，实现内部有状态，另造实例会让 fencing token 与实际执行状态分叉。
func wireAgentRepoDeps(
	db *pgxpool.Pool, deps *agent.AgentServiceDeps, checkpointStore *persistence.PgCheckpointStore,
) {
	if db == nil {
		return
	}
	deps.ResourceEditorRepo = persistence.NewPgResourceEditorRepo(db)
	// 通用产品版本历史（read-only）+ created_by 昵称解析，未装配 fail-closed。
	deps.VersionRepo = versioningpersistence.NewPgVersionRepo(db)
	deps.ActorNameResolver = iampersistence.NewPgActorNameResolver(db)
	deps.LeaseRepo = checkpointStore
}

// wireAgentStreamResume 装配断线续传的流依赖（Stream 输出流 + 控制通道）并注册
// 关闭 hook。执行租约由 wireAgentRepoDeps 与其它 DB deps 一同装配。
//
// 降级：redis 不可用时 StreamStore/ControlBus 保持 nil，application 层 fail closed
// （ExecuteStream 报错），不静默退化成「续传可用」。
func wireAgentStreamResume(c *Container, a *Agent, deps *agent.AgentServiceDeps) {
	if c.Storage == nil || c.Storage.Redis == nil {
		return
	}
	// Pub/Sub 独占一条连接：控制通道与 Stream 命令必须分开实例，否则订阅期间普通
	// 命令会在同一连接上排队饿死（spec §7.1）。Duplicate 返回包装实例，注册进关闭
	// 链的必须是它（自带簿记）；NewControlBus 要的是它内部的裸客户端。
	controlClient := c.Storage.Redis.Duplicate()
	deps.StreamStore = agentstream.NewAgentStreamStore(
		pkgredis.NewStreamStore(c.Storage.Redis.Client()),
		c.Logger,
	)
	deps.ControlBus = agentstream.NewControlBus(controlClient.Client())
	// a.Service 在 buildAgent 中稍后才赋值；hook 是延迟求值的闭包，且 buildAgent 在
	// 赋值之后没有 error 返回路径，故 Shutdown 执行到这里时 a.Service 已就绪。
	c.shutdown = append(c.shutdown, agentStreamShutdownHooks(
		controlClient,
		func() { a.Service.ShutdownStreamRunners() },
	)...)
}

// agentStreamShutdownHooks 返回断线续传的两个关闭 hook，按 **append 顺序** 排列。
//
// Container.Shutdown 逆序执行 hook，所以「最后执行」的 controlClient 关闭排在最前、
// 「最先执行」的 runner 停止排在最后。顺序不可交换：runner 的退出路径仍要写终态帧
// 并操作 control bus，先关 controlClient 会让这些操作打在已关闭的连接上——轻则终态
// 帧/checkpoint 状态写不回，重则在 CancelAll 的 wg.Wait() 上等一个卡死在坏连接上的
// runner，优雅退出挂死（spec §7.4、风险红线第 6 条）。
func agentStreamShutdownHooks(controlClient io.Closer, stopRunners func()) []func(context.Context) error {
	return []func(context.Context) error{
		func(context.Context) error {
			// 后跑：runner 全停之后再关控制通道的独立连接。
			return controlClient.Close()
		},
		func(context.Context) error {
			// 先跑：取消所有在跑的 run 并等待退出。
			stopRunners()
			return nil
		},
	}
}

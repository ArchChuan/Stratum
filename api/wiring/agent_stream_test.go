package wiring

import (
	"context"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// recordingCloser 记录控制通道连接是否已被关闭，用于断言关闭顺序。
type recordingCloser struct {
	closed bool
}

func (c *recordingCloser) Close() error {
	c.closed = true
	return nil
}

// TestAgentStreamShutdownHooksStopRunnersBeforeClosingControl 断言断线续传的关闭顺序。
//
// Container.Shutdown 逆序执行 hook，因此「先停 runner、后关控制通道连接」要求注册
// 顺序与之相反。顺序写反时 runner 的终态帧会打在已关闭的连接上（checkpoint 状态
// 写不回），或在 CancelAll 的 wg.Wait() 上等一个卡死的 runner，优雅退出挂死
// （spec §7.4、风险红线第 6 条）。
func TestAgentStreamShutdownHooksStopRunnersBeforeClosingControl(t *testing.T) {
	control := &recordingCloser{}
	runnersStoppedWhileControlOpen := false

	c := &Container{}
	c.shutdown = append(c.shutdown, agentStreamShutdownHooks(control, func() {
		runnersStoppedWhileControlOpen = !control.closed
	})...)

	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !runnersStoppedWhileControlOpen {
		t.Fatal("runners must stop before the control connection is closed")
	}
	if !control.closed {
		t.Fatal("shutdown chain must close the control connection")
	}
}

// TestWireAgentStreamResumeRegistersHooksOnlyWithRedis 断言装配的降级边界：
// redis 不可用时流依赖保持 nil 且不注册 hook（application 层 fail closed），
// 可用时装配 StreamStore/ControlBus 并注册两个关闭 hook。
func TestWireAgentStreamResumeRegistersHooksOnlyWithRedis(t *testing.T) {
	t.Run("without redis keeps deps nil and registers no hook", func(t *testing.T) {
		c := &Container{}
		deps := agent.AgentServiceDeps{}
		wireAgentStreamResume(c, &Agent{}, &deps)

		if deps.StreamStore != nil || deps.ControlBus != nil {
			t.Fatalf("stream deps must stay nil without redis: %+v", deps)
		}
		if len(c.shutdown) != 0 {
			t.Fatalf("shutdown hooks = %d, want 0", len(c.shutdown))
		}
	})

	t.Run("with redis wires stream deps and two hooks", func(t *testing.T) {
		// 测试专用：构造后不拨号、不关闭（pkgredis.Wrap 无 logger，Close 不是本
		// 用例的被测点），故不调用 Container.Shutdown。
		parent := pkgredis.Wrap(goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"}))
		c := &Container{Storage: &Storage{Redis: parent}}
		deps := agent.AgentServiceDeps{}
		wireAgentStreamResume(c, &Agent{}, &deps)

		if deps.StreamStore == nil {
			t.Error("StreamStore must be wired when redis is available")
		}
		if deps.ControlBus == nil {
			t.Error("ControlBus must be wired when redis is available")
		}
		if len(c.shutdown) != 2 {
			t.Fatalf("shutdown hooks = %d, want 2", len(c.shutdown))
		}
	})
}

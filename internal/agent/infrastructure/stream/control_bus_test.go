package stream_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
)

func newControlBus(t *testing.T) *agentstream.ControlBus {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return agentstream.NewControlBus(rdb)
}

func TestControlBusPublishesViewerAndStop(t *testing.T) {
	bus := newControlBus(t)
	ctx := tenantCtx("acme")
	// runner 必须在 run 启动前订阅好，这里同样先订阅再发布。
	msgs, unsubscribe, err := bus.Subscribe(ctx, "exec-1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	// Pub/Sub 订阅建立是异步的：先确认订阅已生效再发布，否则首条消息会丢。
	if err := bus.WaitSubscribed(ctx, "exec-1"); err != nil {
		t.Fatalf("WaitSubscribed: %v", err)
	}
	if err := bus.PublishViewer(ctx, "exec-1", "viewer-a"); err != nil {
		t.Fatalf("PublishViewer: %v", err)
	}
	if err := bus.PublishStop(ctx, "exec-1"); err != nil {
		t.Fatalf("PublishStop: %v", err)
	}

	got := make([]port.ControlMessage, 0, 2)
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case m := <-msgs:
			got = append(got, m)
		case <-timeout:
			t.Fatalf("timed out; got %+v", got)
		}
	}
	if got[0].Type != port.ControlMessageViewer || got[0].ViewerID != "viewer-a" {
		t.Fatalf("msg[0] = %+v, want viewer viewer-a", got[0])
	}
	if got[1].Type != port.ControlMessageCancel {
		t.Fatalf("msg[1] = %+v, want cancel", got[1])
	}
}

func TestControlBusIsTenantScoped(t *testing.T) {
	// 两个租户的同名 execution_id 必须落在不同通道，否则一个租户的停止指令
	// 会杀掉另一个租户的运行中执行。
	bus := newControlBus(t)
	acme, unsub1, err := bus.Subscribe(tenantCtx("acme"), "shared-exec")
	if err != nil {
		t.Fatal(err)
	}
	defer unsub1()
	if err := bus.WaitSubscribed(tenantCtx("acme"), "shared-exec"); err != nil {
		t.Fatal(err)
	}
	if err := bus.PublishStop(tenantCtx("globex"), "shared-exec"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-acme:
		t.Fatalf("cross-tenant stop leaked: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestControlBusSubscribeFailsClosedWithoutTenant(t *testing.T) {
	bus := newControlBus(t)
	if _, _, err := bus.Subscribe(context.Background(), "exec-1"); err == nil {
		t.Fatal("expected fail-closed error for missing tenant context")
	}
}

func TestControlBusResubscribeSameExecutionDoesNotPanic(t *testing.T) {
	// 同一 execution_id 的第二次 Subscribe 会再次 markReady；F5 重连、双 tab、
	// 跨实例 attach 都走这条路，信号门必须幂等而不是对已关闭 channel 再 close。
	bus := newControlBus(t)
	_, unsub1, err := bus.Subscribe(tenantCtx("acme"), "exec-again")
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}
	defer unsub1()

	msgs, unsub2, err := bus.Subscribe(tenantCtx("acme"), "exec-again")
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	defer unsub2()

	if err := bus.WaitSubscribed(tenantCtx("acme"), "exec-again"); err != nil {
		t.Fatalf("WaitSubscribed: %v", err)
	}
	if err := bus.PublishStop(tenantCtx("acme"), "exec-again"); err != nil {
		t.Fatalf("PublishStop: %v", err)
	}
	// Pub/Sub 是广播：两条订阅都应收到同一条停止指令。
	select {
	case m := <-msgs:
		if m.Type != port.ControlMessageCancel {
			t.Fatalf("msg = %+v, want cancel", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second subscription missed the stop message")
	}
}

func TestControlBusUnsubscribeClosesChannelAndIsIdempotent(t *testing.T) {
	// 取消函数是同步的：返回后消费 goroutine 已退出、消息通道已关闭。
	bus := newControlBus(t)
	msgs, unsubscribe, err := bus.Subscribe(tenantCtx("acme"), "exec-close")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	unsubscribe()
	unsubscribe()

	select {
	case _, ok := <-msgs:
		if ok {
			t.Fatal("received a message after unsubscribe, want closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("message channel not closed after unsubscribe")
	}
}

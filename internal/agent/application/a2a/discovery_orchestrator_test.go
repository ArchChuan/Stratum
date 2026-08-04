package a2a

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDiscoveryRegisterAndQueryPeers(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	if err := d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}}); err != nil {
		t.Fatalf("register = %v", err)
	}
	if err := d.RegisterPeer(context.Background(), testAgent("a2"), []Capability{{Name: "code"}, {Name: "docs"}}); err != nil {
		t.Fatalf("register = %v", err)
	}

	peer := d.GetPeer("a1")
	if peer == nil || peer.Identity.ID != "a1" {
		t.Fatalf("GetPeer = %+v", peer)
	}
	if d.GetPeer("ghost") != nil {
		t.Fatal("ghost peer must be nil")
	}
	if len(d.GetAllPeers()) != 2 {
		t.Fatalf("peers = %d", len(d.GetAllPeers()))
	}
	if peer.HeartbeatInterval != 30*time.Second {
		t.Fatalf("heartbeat interval = %v", peer.HeartbeatInterval)
	}
}

func TestDiscoveryCapabilityMatching(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})
	d.RegisterPeer(context.Background(), testAgent("a2"), []Capability{{Name: "code"}, {Name: "docs"}})

	// 空能力要求 → 全部 peer。
	if got := d.GetPeersWithCapabilities(nil); len(got) != 2 {
		t.Fatalf("empty caps = %d", len(got))
	}
	// 交集匹配。
	if got := d.GetPeersWithCapabilities([]string{"code", "docs"}); len(got) != 1 || got[0].Identity.ID != "a2" {
		t.Fatalf("intersection = %+v", got)
	}
	// 极端情况：无人具备的能力 → 空结果。
	if got := d.GetPeersWithCapabilities([]string{"nope"}); len(got) != 0 {
		t.Fatalf("missing cap = %+v", got)
	}
	// 极端情况：部分具备 → 交集为空。
	if got := d.GetPeersWithCapabilities([]string{"code", "nope"}); len(got) != 0 {
		t.Fatalf("partial = %+v", got)
	}
}

func TestDiscoveryUnregisterPeer(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})

	// 极端情况：不存在的 peer no-op。
	if err := d.UnregisterPeer(context.Background(), "ghost"); err != nil {
		t.Fatalf("unregister ghost = %v", err)
	}

	if err := d.UnregisterPeer(context.Background(), "a1"); err != nil {
		t.Fatalf("unregister = %v", err)
	}
	if d.GetPeer("a1") != nil {
		t.Fatal("peer must be removed")
	}
	// 能力索引同步清理。
	if got := d.GetPeersWithCapabilities([]string{"code"}); len(got) != 0 {
		t.Fatalf("index after unregister = %+v", got)
	}
}

func TestDiscoveryFindBestPeer(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	if _, err := d.FindBestPeer(context.Background(), []string{"code"}, false); !errors.Is(err, ErrNoPeersFound) {
		t.Fatalf("empty err = %v", err)
	}
	d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})
	peer, err := d.FindBestPeer(context.Background(), []string{"code"}, true)
	if err != nil || peer.Identity.ID != "a1" {
		t.Fatalf("best = %+v, %v", peer, err)
	}
}

func TestDiscoverySubscribeEvents(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := d.Subscribe(ctx)

	d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})
	select {
	case ev := <-events:
		if ev.Type != "joined" || ev.Peer.Identity.ID != "a1" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("joined event not received")
	}

	d.UnregisterPeer(context.Background(), "a1")
	select {
	case ev := <-events:
		if ev.Type != "left" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("left event not received")
	}

	// 极端情况：ctx 取消后 channel 被关闭并从订阅列表移除。
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("channel must be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed")
	}
}

func TestDiscoveryUpdateHeartbeatAndCleanup(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	d.RegisterPeer(context.Background(), testAgent("a1"), []Capability{{Name: "code"}})

	// 极端情况：不存在 peer 的 heartbeat no-op 不 panic。
	d.UpdateHeartbeat("ghost")

	d.Cleanup(-time.Hour) // 全部超龄
	if d.GetPeer("a1") != nil {
		t.Fatal("inactive peer must be removed")
	}

	d.RegisterPeer(context.Background(), testAgent("a2"), []Capability{{Name: "code"}})
	d.Cleanup(time.Hour) // 全部活跃
	if d.GetPeer("a2") == nil {
		t.Fatal("active peer must survive")
	}
}

func TestDiscoveryStartCleanupLoopCancels(t *testing.T) {
	// 极端情况：ctx 取消后清理循环退出。
	d := NewDiscoveryService(zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.StartCleanupLoop(ctx, time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup loop must stop on cancel")
	}
}

func TestOrchestratorCreatePlanStrategies(t *testing.T) {
	participants := []AgentIdentity{testAgent("a1"), testAgent("a2"), testAgent("a3")}
	cases := []struct {
		strategy CollaborationStrategy
		check    func(*testing.T, []*TaskStep)
	}{
		{StrategySequential, func(t *testing.T, steps []*TaskStep) {
			if len(steps) != 3 || len(steps[0].Dependencies) != 0 || len(steps[1].Dependencies) != 1 || steps[1].Dependencies[0] != steps[0].ID {
				t.Fatalf("sequential deps = %+v", steps)
			}
		}},
		{StrategyParallel, func(t *testing.T, steps []*TaskStep) {
			if len(steps) != 3 || len(steps[2].Dependencies) != 0 {
				t.Fatalf("parallel deps = %+v", steps)
			}
		}},
		{StrategyHierarchical, func(t *testing.T, steps []*TaskStep) {
			if len(steps) != 3 || len(steps[1].Dependencies) != 1 || steps[1].Dependencies[0] != steps[0].ID {
				t.Fatalf("hierarchical deps = %+v", steps)
			}
		}},
		{StrategyPipeline, func(t *testing.T, steps []*TaskStep) {
			if len(steps) != 3 || steps[1].Dependencies[0] != steps[0].ID {
				t.Fatalf("pipeline deps = %+v", steps)
			}
		}},
		{StrategySwarm, func(t *testing.T, steps []*TaskStep) {
			if len(steps) != 3 || steps[0].Name != "Agent a1_swarm_agent" || len(steps[0].Dependencies) != 0 {
				t.Fatalf("swarm = %+v", steps[0])
			}
		}},
		{"bogus", func(t *testing.T, steps []*TaskStep) {
			// 极端情况：未知策略回退 sequential。
			if len(steps) != 3 || len(steps[1].Dependencies) != 1 {
				t.Fatalf("fallback deps = %+v", steps)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(string(tc.strategy), func(t *testing.T) {
			o := NewOrchestrator(zap.NewNop())
			plan, err := o.CreatePlan(context.Background(), "collab-1", "task", tc.strategy, participants)
			if err != nil {
				t.Fatalf("create = %v", err)
			}
			if plan.Status != "created" || plan.CollaborationID != "collab-1" || len(plan.Steps) != 3 {
				t.Fatalf("plan = %+v", plan)
			}
			tc.check(t, plan.Steps)
		})
	}
}

func TestOrchestratorHierarchicalEmptyParticipants(t *testing.T) {
	// 极端情况：空 participants → 空 steps 不 panic。
	o := NewOrchestrator(zap.NewNop())
	plan, err := o.CreatePlan(context.Background(), "c", "t", StrategyHierarchical, nil)
	if err != nil || len(plan.Steps) != 0 {
		t.Fatalf("empty plan = %+v, %v", plan, err)
	}
}

func TestOrchestratorPlanAndContextQueries(t *testing.T) {
	o := NewOrchestrator(zap.NewNop())
	plan, _ := o.CreatePlan(context.Background(), "c1", "t", StrategySequential, []AgentIdentity{testAgent("a1")})

	if _, err := o.GetPlan(plan.ID); err != nil {
		t.Fatalf("get plan = %v", err)
	}
	if _, err := o.GetPlan("ghost"); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("ghost plan err = %v", err)
	}
	if _, err := o.GetSharedContext("c1"); err != nil {
		t.Fatalf("get ctx = %v", err)
	}
	if _, err := o.GetSharedContext("ghost"); !errors.Is(err, ErrContextNotFound) {
		t.Fatalf("ghost ctx err = %v", err)
	}
}

func TestOrchestratorUpdateContext(t *testing.T) {
	o := NewOrchestrator(zap.NewNop())
	o.CreatePlan(context.Background(), "c1", "t", StrategySequential, []AgentIdentity{testAgent("a1")})

	o.UpdateContext("c1", "key", 1)
	ctx, err := o.GetSharedContext("c1")
	if err != nil || ctx.Data["key"] != 1 || ctx.Version != 1 {
		t.Fatalf("ctx = %+v, %v", ctx, err)
	}
	// 极端情况：未知 collaboration no-op 不 panic。
	o.UpdateContext("ghost", "k", "v")
}

func TestOrchestratorMarkStepComplete(t *testing.T) {
	o := NewOrchestrator(zap.NewNop())
	plan, _ := o.CreatePlan(context.Background(), "c1", "t", StrategySequential, []AgentIdentity{testAgent("a1"), testAgent("a2")})

	// 极端情况：plan 缺失。
	if err := o.MarkStepComplete("ghost", "step", nil); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("ghost plan err = %v", err)
	}
	// 极端情况：step 缺失。
	if err := o.MarkStepComplete(plan.ID, "nope", nil); !errors.Is(err, ErrStepNotFound) {
		t.Fatalf("ghost step err = %v", err)
	}
	// 成功。
	if err := o.MarkStepComplete(plan.ID, plan.Steps[0].ID, map[string]interface{}{"out": 1}); err != nil {
		t.Fatalf("complete = %v", err)
	}
	step := plan.Steps[0]
	if step.Status != taskStatusCompleted || step.Output["out"] != 1 || step.CompletedAt.IsZero() {
		t.Fatalf("step = %+v", step)
	}
}

func TestOrchestratorCleanup(t *testing.T) {
	o := NewOrchestrator(zap.NewNop())
	plan, _ := o.CreatePlan(context.Background(), "c1", "t", StrategySequential, []AgentIdentity{testAgent("a1")})

	// 极端情况：非 completed 的 plan 不清理。
	o.Cleanup(-time.Hour)
	if _, err := o.GetPlan(plan.ID); err != nil {
		t.Fatal("created plan must survive")
	}

	_ = o.MarkStepComplete(plan.ID, plan.Steps[0].ID, nil)
	plan.mu.Lock()
	plan.Status = taskStatusCompleted
	plan.mu.Unlock()
	o.Cleanup(-time.Hour)
	if _, err := o.GetPlan(plan.ID); err == nil {
		t.Fatal("completed plan must be cleaned")
	}
	if _, err := o.GetSharedContext("c1"); err == nil {
		t.Fatal("collaboration context must be cleaned with plan")
	}
}

func TestA2AErrorUnwrapAndNewError(t *testing.T) {
	cause := errors.New("root cause")
	err := NewError(ErrorTypeProtocol, "failed", cause)
	if err.Error() != "[protocol] failed: root cause" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("Unwrap must expose cause")
	}
	// 极端情况：无 cause。
	if got := NewError(ErrorTypeTimeout, "took long", nil).Error(); got != "[timeout] took long" {
		t.Fatalf("no-cause Error() = %q", got)
	}
}

func TestDiscoveryConcurrentAccess(t *testing.T) {
	d := NewDiscoveryService(zap.NewNop())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = d.RegisterPeer(context.Background(), testAgent(string(rune('a'+i))), []Capability{{Name: "code"}})
			_ = d.GetPeersWithCapabilities([]string{"code"})
			d.UpdateHeartbeat(string(rune('a' + i)))
			d.GetAllPeers()
		}(i)
	}
	wg.Wait()
}

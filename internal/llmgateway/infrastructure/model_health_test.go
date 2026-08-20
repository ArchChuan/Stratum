package infrastructure_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// fakeClock 是可推进的测试时钟，驱动健康状态机的时间相关转移。
type fakeClock struct {
	cur time.Time
}

func newFakeClock(t0 time.Time) *fakeClock { return &fakeClock{cur: t0} }

func (c *fakeClock) Now() time.Time { return c.cur }

func (c *fakeClock) Advance(d time.Duration) { c.cur = c.cur.Add(d) }

func TestHealthRegistry_FailureThresholdTransitions(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	const model = "qwen-plus"

	// 初始未记录 = healthy
	require.Equal(t, infrastructure.ModelHealthHealthy, reg.Get(model).Status)

	// healthy → 连续失败达阈值 → degraded
	for i := 0; i < 5; i++ {
		reg.RecordFailure(model, errors.New("upstream 500"))
	}
	require.Equal(t, infrastructure.ModelHealthDegraded, reg.Get(model).Status)
	require.Equal(t, 5, reg.Get(model).Failures)

	// degraded 后一次成功 → 立即回 healthy，计数清零
	reg.RecordSuccess(model)
	require.Equal(t, infrastructure.ModelHealthHealthy, reg.Get(model).Status)
	require.Zero(t, reg.Get(model).Failures)
}

func TestHealthRegistry_DegradedToUnhealthyAfterRecoveryWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	const model = "qwen-plus"

	for i := 0; i < 5; i++ {
		reg.RecordFailure(model, errors.New("upstream 500"))
	}
	require.Equal(t, infrastructure.ModelHealthDegraded, reg.Get(model).Status)

	// 窗口内继续失败：仍 degraded
	clock.Advance(10 * time.Second)
	reg.RecordFailure(model, errors.New("upstream 500"))
	require.Equal(t, infrastructure.ModelHealthDegraded, reg.Get(model).Status)

	// 超过 recovery 窗口（30s）持续失败 → unhealthy
	clock.Advance(25 * time.Second)
	reg.RecordFailure(model, errors.New("upstream 500"))
	require.Equal(t, infrastructure.ModelHealthUnhealthy, reg.Get(model).Status)
}

func TestHealthRegistry_UnhealthyToHalfOpenProbe(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	const model = "qwen-plus"

	// 驱动到 unhealthy
	for i := 0; i < 5; i++ {
		reg.RecordFailure(model, errors.New("upstream 500"))
	}
	clock.Advance(40 * time.Second)
	reg.RecordFailure(model, errors.New("upstream 500"))
	require.Equal(t, infrastructure.ModelHealthUnhealthy, reg.Get(model).Status)

	// 未到 recovery 窗口：不允许探测
	clock.Advance(10 * time.Second)
	require.False(t, reg.AllowProbe(model))

	// 超过窗口：自动转 halfOpen 并放行单次探测
	clock.Advance(25 * time.Second)
	require.True(t, reg.AllowProbe(model))
	state := reg.Get(model)
	require.Equal(t, infrastructure.ModelHealthHalfOpen, state.Status)

	// halfOpen 已在探测中：拒绝并发叠加
	require.False(t, reg.AllowProbe(model))

	// 探测成功 → healthy；计数清零
	reg.RecordProbe(model, true, nil)
	require.Equal(t, infrastructure.ModelHealthHealthy, reg.Get(model).Status)
	require.Zero(t, reg.Get(model).Failures)
}

func TestHealthRegistry_HalfOpenProbeFailureReturnsUnhealthy(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	const model = "qwen-plus"

	for i := 0; i < 5; i++ {
		reg.RecordFailure(model, errors.New("upstream 500"))
	}
	clock.Advance(40 * time.Second)
	reg.RecordFailure(model, errors.New("upstream 500"))
	clock.Advance(31 * time.Second)

	require.True(t, reg.AllowProbe(model))
	require.Equal(t, infrastructure.ModelHealthHalfOpen, reg.Get(model).Status)

	// 探测失败 → 回到 unhealthy
	reg.RecordProbe(model, false, errors.New("probe 503"))
	require.Equal(t, infrastructure.ModelHealthUnhealthy, reg.Get(model).Status)
}

func TestHealthRegistry_ProbeFailureMarksDegradedOnHealthy(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	const model = "idle-embed-model"

	// 空闲模型无调用信号：探活失败即标记 degraded（覆盖空窗）
	reg.RecordProbe(model, false, errors.New("probe timeout"))
	require.Equal(t, infrastructure.ModelHealthDegraded, reg.Get(model).Status)

	// 探活恢复 → healthy
	reg.RecordProbe(model, true, nil)
	require.Equal(t, infrastructure.ModelHealthHealthy, reg.Get(model).Status)
}

func TestHealthRegistry_AllReturnsSnapshots(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	reg.RecordSuccess("model-a")
	for i := 0; i < 5; i++ {
		reg.RecordFailure("model-b", errors.New("upstream 500"))
	}

	all := reg.All()
	require.Len(t, all, 2)
	byModel := make(map[string]infrastructure.ModelHealthState, len(all))
	for _, s := range all {
		byModel[s.Model] = s
	}
	require.Equal(t, infrastructure.ModelHealthHealthy, byModel["model-a"].Status)
	require.Equal(t, infrastructure.ModelHealthDegraded, byModel["model-b"].Status)
}

func TestHealthRegistry_LastErrorIsRedacted(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	reg := infrastructure.NewHealthRegistry(clock.Now)
	// 上游错误 body 已脱敏，但健康 registry 对可能含 authorization/token
	// 键值的错误再做一道纵深防御。真实形式为 "authorization: Bearer sk-..."。
	reg.RecordFailure("model-a", errors.New("provider auth failed: authorization: Bearer sk-abc123token"))
	state := reg.Get("model-a")
	require.NotContains(t, state.LastError, "sk-abc123token")
	require.NotEmpty(t, state.LastError)
}

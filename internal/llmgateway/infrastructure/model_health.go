package infrastructure

import (
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/pkg/safetext"
)

// ModelHealth 是模型健康状态四态：healthy / degraded / unhealthy / halfOpen。
// 由被动信号（调用成功/失败）与主动信号（探活）共同驱动；resolver、UI、告警
// 统一消费。映射到 providerBreaker 三态：closed→healthy|degraded（按连续失败
// 计数分档）、open→unhealthy、half-open 保留半开语义。
type ModelHealth string

const (
	ModelHealthHealthy   ModelHealth = "healthy"
	ModelHealthDegraded  ModelHealth = "degraded"
	ModelHealthUnhealthy ModelHealth = "unhealthy"
	ModelHealthHalfOpen  ModelHealth = "half_open"
)

// ModelHealthState 是单个模型健康状态的不可变快照（供 UI/告警展示）。
type ModelHealthState struct {
	Model       string
	Status      ModelHealth
	Failures    int
	LastCheckAt time.Time
	LastError   string
}

// modelHealth 是单个模型的健康状态机条目。degradedAt 记录进入 degraded 的时刻，
// 用于「持续失败 ≥ recovery 窗口」判定 degraded→unhealthy；probing 标记
// halfOpen 期间的单次探测放行。
type modelHealth struct {
	status      ModelHealth
	failures    int
	degradedAt  time.Time
	lastFailure time.Time
	lastCheckAt time.Time
	lastError   string
	probing     bool
}

// HealthRegistry 维护 per-model 健康状态。时钟注入 now（默认 time.Now），
// 测试可控地推进时间驱动 degraded→unhealthy→halfOpen 转移。
type HealthRegistry struct {
	now       func() time.Time
	mu        sync.RWMutex
	models    map[string]*modelHealth
	threshold int
	recovery  time.Duration
}

// NewHealthRegistry 构造健康 registry。now 为 nil 时使用 time.Now；
// threshold/recovery 复用 providerBreaker 的熔断阈值，保证被动熔断与健康
// 状态机口径一致。
func NewHealthRegistry(now func() time.Time) *HealthRegistry {
	if now == nil {
		now = time.Now
	}
	return &HealthRegistry{
		now:       now,
		models:    make(map[string]*modelHealth),
		threshold: cbFailureThreshold,
		recovery:  cbRecoveryTimeout,
	}
}

// entry 返回模型的状态机条目，不存在则初始化 healthy。
func (r *HealthRegistry) entry(model string) *modelHealth {
	e, ok := r.models[model]
	if !ok {
		e = &modelHealth{status: ModelHealthHealthy}
		r.models[model] = e
	}
	return e
}

// RecordSuccess 记录一次成功调用：状态回 healthy，清空失败计数与错误。
func (r *HealthRegistry) RecordSuccess(model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry(model)
	e.status = ModelHealthHealthy
	e.failures = 0
	e.lastCheckAt = r.now()
	e.lastError = ""
	e.probing = false
}

// RecordFailure 记录一次失败调用，按当前状态转移：
//   - healthy → 连续失败 ≥ threshold → degraded
//   - degraded → 持续失败 ≥ recovery 窗口 → unhealthy
//   - halfOpen → 探测失败 → unhealthy
//   - unhealthy → 保持，等待 AllowProbe 到期放行探测
//
// 错误信息经 RedactCredentials 脱敏后存储，禁止 API key / token 落入状态。
func (r *HealthRegistry) RecordFailure(model string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry(model)
	now := r.now()
	e.failures++
	e.lastFailure = now
	e.lastCheckAt = now
	if err != nil {
		e.lastError = safetext.RedactCredentials(err.Error())
	}
	switch e.status {
	case ModelHealthHealthy:
		if e.failures >= r.threshold {
			e.status = ModelHealthDegraded
			e.degradedAt = now
		}
	case ModelHealthDegraded:
		if now.Sub(e.degradedAt) >= r.recovery {
			e.status = ModelHealthUnhealthy
		}
	case ModelHealthHalfOpen:
		e.status = ModelHealthUnhealthy
		e.probing = false
	case ModelHealthUnhealthy:
		// 保持；由 AllowProbe 到期放行探测。
	}
}

// RecordProbe 记录一次主动探活结果。成功等同 RecordSuccess。失败比被动
// RecordFailure 更敏感：探活本身就是可达性确认，healthy 状态单次失败即转
// degraded（覆盖空闲模型无调用信号的空窗，无需等待被动阈值）；degraded/
// halfOpen/unhealthy 的累积与转移语义复用 RecordFailure。
func (r *HealthRegistry) RecordProbe(model string, ok bool, err error) {
	if ok {
		r.RecordSuccess(model)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry(model)
	now := r.now()
	e.failures++
	e.lastFailure = now
	e.lastCheckAt = now
	if err != nil {
		e.lastError = safetext.RedactCredentials(err.Error())
	}
	switch e.status {
	case ModelHealthHealthy:
		e.status = ModelHealthDegraded
		e.degradedAt = now
	case ModelHealthDegraded:
		if now.Sub(e.degradedAt) >= r.recovery {
			e.status = ModelHealthUnhealthy
		}
	case ModelHealthHalfOpen:
		e.status = ModelHealthUnhealthy
		e.probing = false
	case ModelHealthUnhealthy:
		// 保持；由 AllowProbe 到期放行探测。
	}
}

// AllowProbe 判定探活 worker 是否应放行一次探测请求：
//   - unhealthy 超过 recovery 窗口 → 自动转 halfOpen 并放行单次探测；
//   - halfOpen 已在探测中 → 拒绝并发叠加探测；
//   - healthy / degraded → 允许（探活覆盖空闲空窗）。
func (r *HealthRegistry) AllowProbe(model string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry(model)
	now := r.now()
	switch e.status {
	case ModelHealthUnhealthy:
		if now.Sub(e.lastFailure) >= r.recovery {
			e.status = ModelHealthHalfOpen
			e.probing = true
			return true
		}
		return false
	case ModelHealthHalfOpen:
		if e.probing {
			return false
		}
		e.probing = true
		return true
	default:
		return true
	}
}

// Get 返回模型健康状态的不可变快照。未记录状态的模型视为 healthy。
func (r *HealthRegistry) Get(model string) ModelHealthState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.models[model]
	if !ok {
		return ModelHealthState{Model: model, Status: ModelHealthHealthy}
	}
	return ModelHealthState{
		Model:       model,
		Status:      e.status,
		Failures:    e.failures,
		LastCheckAt: e.lastCheckAt,
		LastError:   e.lastError,
	}
}

// All 返回全部已记录模型的状态快照（未记录模型不返回；调用方按 healthy 处理）。
func (r *HealthRegistry) All() []ModelHealthState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelHealthState, 0, len(r.models))
	for name, e := range r.models {
		out = append(out, ModelHealthState{
			Model:       name,
			Status:      e.status,
			Failures:    e.failures,
			LastCheckAt: e.lastCheckAt,
			LastError:   e.lastError,
		})
	}
	return out
}

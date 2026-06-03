// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"sync"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"go.uber.org/zap"
)

// CBState 熔断器状态
type CBState int

const (
	CBClosed   CBState = iota // 正常，允许请求
	CBOpen                    // 熔断，拒绝请求
	CBHalfOpen                // 半开，允许一个探测请求
)

func (s CBState) String() string {
	switch s {
	case CBClosed:
		return "closed"
	case CBOpen:
		return "open"
	case CBHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	FailureThreshold int           // 连续失败次数触发熔断，默认 5
	RecoveryTimeout  time.Duration // 熔断后恢复等待时间，默认 30s
}

func defaultCBConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		FailureThreshold: 5,
		RecoveryTimeout:  30 * time.Second,
	}
}

type breaker struct {
	state         CBState
	failures      int
	lastFailureAt time.Time
	probing       bool // HalfOpen 时是否已有探测请求在途
	mu            sync.Mutex
}

// CircuitBreakerManager 管理所有 skill 的熔断器
type CircuitBreakerManager struct {
	breakers map[string]*breaker
	config   CircuitBreakerConfig
	metrics  *observability.PrometheusMetrics
	logger   *zap.Logger
	mu       sync.RWMutex
}

func newCircuitBreakerManager(cfg CircuitBreakerConfig, metrics *observability.PrometheusMetrics, logger *zap.Logger) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*breaker),
		config:   cfg,
		metrics:  metrics,
		logger:   logger.Named("circuit_breaker"),
	}
}

func (m *CircuitBreakerManager) getOrCreate(skillID string) *breaker {
	m.mu.RLock()
	b, ok := m.breakers[skillID]
	m.mu.RUnlock()
	if ok {
		return b
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok = m.breakers[skillID]; ok {
		return b
	}
	b = &breaker{state: CBClosed}
	m.breakers[skillID] = b
	return b
}

// Allow 检查是否允许请求通过；HalfOpen 时只允许第一个探测请求
func (m *CircuitBreakerManager) Allow(skillID string) bool {
	b := m.getOrCreate(skillID)
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case CBClosed:
		return true
	case CBOpen:
		if time.Since(b.lastFailureAt) >= m.config.RecoveryTimeout {
			b.state = CBHalfOpen
			b.probing = true
			m.logStateChange(skillID, CBOpen, CBHalfOpen)
			return true
		}
		return false
	case CBHalfOpen:
		if !b.probing {
			b.probing = true
			return true
		}
		return false
	}
	return false
}

// RecordSuccess 记录成功，重置熔断器
func (m *CircuitBreakerManager) RecordSuccess(skillID string) {
	b := m.getOrCreate(skillID)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != CBClosed {
		m.logStateChange(skillID, b.state, CBClosed)
	}
	b.state = CBClosed
	b.failures = 0
	b.probing = false
	m.reportState(skillID, CBClosed)
}

// RecordFailure 记录失败，可能触发熔断
func (m *CircuitBreakerManager) RecordFailure(skillID string) {
	b := m.getOrCreate(skillID)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.probing = false
	switch b.state {
	case CBClosed:
		b.failures++
		if b.failures >= m.config.FailureThreshold {
			b.state = CBOpen
			b.lastFailureAt = time.Now()
			m.logStateChange(skillID, CBClosed, CBOpen)
			m.reportState(skillID, CBOpen)
		}
	case CBHalfOpen:
		b.state = CBOpen
		b.lastFailureAt = time.Now()
		m.logStateChange(skillID, CBHalfOpen, CBOpen)
		m.reportState(skillID, CBOpen)
	}
}

func (m *CircuitBreakerManager) logStateChange(skillID string, from, to CBState) {
	m.logger.Warn("circuit breaker state changed",
		zap.String("skill_id", skillID),
		zap.String("from", from.String()),
		zap.String("to", to.String()),
	)
}

func (m *CircuitBreakerManager) reportState(skillID string, state CBState) {
	if m.metrics == nil {
		return
	}
	m.metrics.SetSkillCircuitBreakerState(skillID, float64(state))
}

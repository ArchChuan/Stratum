package gateway

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestCB() *CircuitBreakerManager {
	return newCircuitBreakerManager(CircuitBreakerConfig{
		FailureThreshold: 3,
		RecoveryTimeout:  50 * time.Millisecond,
	}, nil, zap.NewNop())
}

func TestCircuitBreaker_ClosedAllowsRequests(t *testing.T) {
	cb := newTestCB()
	if !cb.Allow("skill:a") {
		t.Error("expected closed breaker to allow requests")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := newTestCB()
	for i := 0; i < 3; i++ {
		cb.Allow("skill:a")
		cb.RecordFailure("skill:a")
	}
	if cb.Allow("skill:a") {
		t.Error("expected open breaker to reject requests")
	}
}

func TestCircuitBreaker_HalfOpenAfterRecovery(t *testing.T) {
	cb := newTestCB()
	for i := 0; i < 3; i++ {
		cb.Allow("skill:a")
		cb.RecordFailure("skill:a")
	}
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow("skill:a") {
		t.Error("expected half-open breaker to allow one probe request")
	}
	// 第二个请求应被拒绝（探测已在途）
	if cb.Allow("skill:a") {
		t.Error("expected second request to be rejected in half-open state")
	}
}

func TestCircuitBreaker_ClosesAfterSuccessInHalfOpen(t *testing.T) {
	cb := newTestCB()
	for i := 0; i < 3; i++ {
		cb.Allow("skill:a")
		cb.RecordFailure("skill:a")
	}
	time.Sleep(60 * time.Millisecond)
	cb.Allow("skill:a")
	cb.RecordSuccess("skill:a")
	if !cb.Allow("skill:a") {
		t.Error("expected breaker to close after successful probe")
	}
}

func TestCircuitBreaker_ReopensAfterFailureInHalfOpen(t *testing.T) {
	cb := newTestCB()
	for i := 0; i < 3; i++ {
		cb.Allow("skill:a")
		cb.RecordFailure("skill:a")
	}
	time.Sleep(60 * time.Millisecond)
	cb.Allow("skill:a")
	cb.RecordFailure("skill:a")
	if cb.Allow("skill:a") {
		t.Error("expected breaker to reopen after failed probe")
	}
}

func TestCircuitBreaker_IndependentPerSkill(t *testing.T) {
	cb := newTestCB()
	for i := 0; i < 3; i++ {
		cb.Allow("skill:a")
		cb.RecordFailure("skill:a")
	}
	if !cb.Allow("skill:b") {
		t.Error("skill:b should be unaffected by skill:a failures")
	}
}

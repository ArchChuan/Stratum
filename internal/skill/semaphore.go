package skill

import (
	"context"
	"errors"
	"sync"
)

// ErrConcurrencyLimit is returned when the global or per-tenant concurrency cap is reached.
var ErrConcurrencyLimit = errors.New("concurrency limit reached")

// Semaphore enforces a global execution cap and an optional per-tenant sub-limit.
type Semaphore struct {
	global     chan struct{}
	perTenant  int
	tenantSems sync.Map // tenantID -> chan struct{}
}

// NewSemaphore creates a Semaphore with a global cap and a per-tenant cap.
// perTenant ≤ 0 disables per-tenant limiting.
func NewSemaphore(globalCap, perTenant int) *Semaphore {
	return &Semaphore{
		global:    make(chan struct{}, globalCap),
		perTenant: perTenant,
	}
}

// Acquire acquires both the global and per-tenant slots.
// Returns ErrConcurrencyLimit immediately if either slot is unavailable.
func (s *Semaphore) Acquire(ctx context.Context, tenantID string) error {
	select {
	case s.global <- struct{}{}:
	default:
		return ErrConcurrencyLimit
	}

	if s.perTenant > 0 && tenantID != "" {
		tsem := s.tenantSem(tenantID)
		select {
		case tsem <- struct{}{}:
		default:
			<-s.global // release global slot we just acquired
			return ErrConcurrencyLimit
		}
	}

	return nil
}

// Release releases previously acquired slots.
func (s *Semaphore) Release(tenantID string) {
	<-s.global

	if s.perTenant > 0 && tenantID != "" {
		if v, ok := s.tenantSems.Load(tenantID); ok {
			<-v.(chan struct{})
		}
	}
}

func (s *Semaphore) tenantSem(tenantID string) chan struct{} {
	v, _ := s.tenantSems.LoadOrStore(tenantID, make(chan struct{}, s.perTenant))
	return v.(chan struct{})
}

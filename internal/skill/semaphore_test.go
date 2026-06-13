package skill

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestSemaphore_AcquireRelease(t *testing.T) {
	s := NewSemaphore(2, 0)
	ctx := context.Background()

	if err := s.Acquire(ctx, "t1"); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := s.Acquire(ctx, "t1"); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	if err := s.Acquire(ctx, "t1"); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want ErrConcurrencyLimit, got %v", err)
	}

	s.Release("t1")
	if err := s.Acquire(ctx, "t1"); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
}

func TestSemaphore_PerTenantLimit(t *testing.T) {
	s := NewSemaphore(10, 2)
	ctx := context.Background()

	if err := s.Acquire(ctx, "t1"); err != nil {
		t.Fatalf("1st: %v", err)
	}
	if err := s.Acquire(ctx, "t1"); err != nil {
		t.Fatalf("2nd: %v", err)
	}
	if err := s.Acquire(ctx, "t1"); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want limit for t1, got %v", err)
	}

	// different tenant should still succeed
	if err := s.Acquire(ctx, "t2"); err != nil {
		t.Fatalf("t2 acquire: %v", err)
	}
}

func TestSemaphore_Concurrent(t *testing.T) {
	const globalCap = 5
	s := NewSemaphore(globalCap, 3)
	ctx := context.Background()

	var mu sync.Mutex
	acquired := 0
	var wg sync.WaitGroup

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Acquire(ctx, "t1"); err == nil {
				mu.Lock()
				acquired++
				mu.Unlock()
				s.Release("t1")
			}
		}()
	}
	wg.Wait()

	if acquired == 0 {
		t.Fatal("no goroutines acquired the semaphore")
	}
}

func TestSemaphore_NoPerTenantLimit(t *testing.T) {
	s := NewSemaphore(5, 0)
	ctx := context.Background()

	// perTenant=0 means no per-tenant sub-limit; global is the only constraint
	for i := range 5 {
		if err := s.Acquire(ctx, "t1"); err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
	}
	if err := s.Acquire(ctx, "t1"); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want global limit, got %v", err)
	}
}

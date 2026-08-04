package reqctx

import (
	"context"
	"testing"
)

func TestTraceIDRoundTrip(t *testing.T) {
	ctx := WithTraceID(context.Background(), "trace-123")
	if got := TraceIDFromContext(ctx); got != "trace-123" {
		t.Errorf("expected trace-123, got %q", got)
	}
}

func TestTraceIDFromContextMissing(t *testing.T) {
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTraceIDFromContextWrongType(t *testing.T) {
	// 极端情况：ctx 中同名 key 存了非 string 值，类型断言必须返回零值而非 panic。
	ctx := context.WithValue(context.Background(), traceIDKey{}, 42)
	if got := TraceIDFromContext(ctx); got != "" {
		t.Errorf("expected empty string for wrong-typed value, got %q", got)
	}
}

func TestTenantIDRoundTrip(t *testing.T) {
	ctx := WithTenantID(context.Background(), "tenant-9")
	if got := TenantIDFromContext(ctx); got != "tenant-9" {
		t.Errorf("expected tenant-9, got %q", got)
	}
}

func TestTenantIDFromContextMissing(t *testing.T) {
	if got := TenantIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTenantIDFromContextWrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), tenantIDKey{}, []string{"x"})
	if got := TenantIDFromContext(ctx); got != "" {
		t.Errorf("expected empty string for wrong-typed value, got %q", got)
	}
}

func TestKeysAreIsolated(t *testing.T) {
	// 极端情况：两个 key 必须互不干扰（trace 值不能污染 tenant 读取）。
	ctx := WithTraceID(WithTenantID(context.Background(), "t1"), "tr1")
	if got := TenantIDFromContext(ctx); got != "t1" {
		t.Errorf("expected t1, got %q", got)
	}
	if got := TraceIDFromContext(ctx); got != "tr1" {
		t.Errorf("expected tr1, got %q", got)
	}
}

func TestValuesSurviveParentCancellation(t *testing.T) {
	// 极端情况：WithCancel 包装后值仍可读（context 值查找不经过错误通道）。
	ctx, cancel := context.WithCancel(WithTraceID(context.Background(), "tr-cancel"))
	cancel()
	if got := TraceIDFromContext(ctx); got != "tr-cancel" {
		t.Errorf("expected tr-cancel after cancel, got %q", got)
	}
}

package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

// statusError 模拟带 HTTP 状态码的 provider 错误（如协议层包装的 429/5xx）。
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string   { return e.msg }
func (e *statusError) StatusCode() int { return e.code }

func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not transient", err: nil, want: false},
		{name: "rate limited 429 is transient", err: &statusError{code: 429, msg: "rate limited"}, want: true},
		{name: "5xx is transient", err: &statusError{code: 500, msg: "internal"}, want: true},
		{name: "502 is transient", err: &statusError{code: 502, msg: "bad gateway"}, want: true},
		{name: "4xx is permanent", err: &statusError{code: 400, msg: "bad request"}, want: false},
		{name: "401 is permanent", err: &statusError{code: 401, msg: "unauthorized"}, want: false},
		{name: "404 is permanent", err: &statusError{code: 404, msg: "not found"}, want: false},
		{name: "deadline exceeded is permanent", err: context.DeadlineExceeded, want: false},
		{name: "wrapped deadline is permanent", err: fmt.Errorf("call: %w", context.DeadlineExceeded), want: false},
		{name: "canceled never triggers fallback", err: context.Canceled, want: false},
		{name: "wrapped canceled never triggers fallback", err: fmt.Errorf("call: %w", context.Canceled), want: false},
		{name: "connection refused is transient", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, want: true},
		{name: "generic net.OpError is transient", err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("reset")}, want: true},
		{name: "timeout net.Error is transient", err: &timeoutNetErr{timeout: true}, want: true},
		{name: "connection refused text is transient", err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused"), want: true},
		{name: "plain error is permanent", err: errors.New("provider error"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient(tc.err); got != tc.want {
				t.Fatalf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsTransient_DeadlineExceededIsPermanent 验证 DeadlineExceeded 必须判定为
// 永久错误：等待无意义，继续重试只会叠加时延，fallback 链应 fail-fast。
func TestIsTransient_DeadlineExceededIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool // transient?
	}{
		{name: "canceled is permanent", err: context.Canceled, want: false},
		{name: "deadline exceeded is permanent", err: context.DeadlineExceeded, want: false},
		{name: "wrapped deadline is permanent", err: fmt.Errorf("upstream: %w", context.DeadlineExceeded), want: false},
		// http.Client timeout 包装链：url.Error → net.OpError → DeadlineExceeded
		// 必须判定永久，否则 60s client timeout 仍被当瞬态重试。
		{name: "http client timeout chain is permanent",
			err:  &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Err: context.DeadlineExceeded}},
			want: false},
		{name: "net timeout is transient", err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "timeout"}}, want: true},
		{name: "status 429 is transient", err: &statusError{code: 429, msg: "rate limited"}, want: true},
		{name: "status 503 is transient", err: &statusError{code: 503, msg: "service unavailable"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient(tc.err); got != tc.want {
				t.Fatalf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsContextLengthExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "bare sentinel", err: ErrContextLengthExceeded, want: true},
		{name: "wrapped", err: fmt.Errorf("complete: %w", ErrContextLengthExceeded), want: true},
		{name: "other 400", err: fmt.Errorf("complete: status 400: schema mismatch"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContextLengthExceeded(tc.err); got != tc.want {
				t.Fatalf("IsContextLengthExceeded(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestContextLengthExceededProbe 验证 agent 层 duck-typing 探测协议：
// 经 %w 包装链 errors.As 可分别命中 ContextLengthExceeded()（Task 9 降级
// 探测）与 Permanent()（permanentMarker）标记。
func TestContextLengthExceededProbe(t *testing.T) {
	err := fmt.Errorf("complete: %w", ErrContextLengthExceeded)
	var cle interface{ ContextLengthExceeded() bool }
	if !errors.As(err, &cle) {
		t.Fatal("wrapped error must expose ContextLengthExceeded() marker")
	}
	if !cle.ContextLengthExceeded() {
		t.Fatal("ContextLengthExceeded() must report true")
	}
	var perm interface{ Permanent() bool }
	if !errors.As(err, &perm) {
		t.Fatal("wrapped error must expose Permanent() marker")
	}
	if !perm.Permanent() {
		t.Fatal("Permanent() must report true")
	}
}

// timeoutNetErr 模拟实现 net.Error 的超时错误。
type timeoutNetErr struct{ timeout bool }

func (e *timeoutNetErr) Error() string   { return "timed out" }
func (e *timeoutNetErr) Timeout() bool   { return e.timeout }
func (e *timeoutNetErr) Temporary() bool { return false }

func TestMarkPermanent(t *testing.T) {
	inner := errors.New("boom")
	wrapped := markPermanent(fmt.Errorf("chain: %w", inner))
	if !IsPermanent(wrapped) {
		t.Fatal("expected wrapped error to be permanent")
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("permanent wrap must preserve errors.Is chain")
	}
	if !errors.As(wrapped, new(interface{ Permanent() bool })) {
		t.Fatal("permanent wrap must expose Permanent() marker")
	}
	// 已永久的不再嵌套。
	if again := markPermanent(wrapped); IsPermanent(again) && again != wrapped {
		t.Fatal("markPermanent must be idempotent")
	}
	// nil 不包装。
	if markPermanent(nil) != nil {
		t.Fatal("markPermanent(nil) must stay nil")
	}
}

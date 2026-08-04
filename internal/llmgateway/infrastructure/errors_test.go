package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net"
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
		{name: "deadline exceeded is transient", err: context.DeadlineExceeded, want: true},
		{name: "wrapped deadline is transient", err: fmt.Errorf("call: %w", context.DeadlineExceeded), want: true},
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

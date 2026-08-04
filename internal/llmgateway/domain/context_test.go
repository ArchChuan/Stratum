package domain

import (
	"context"
	"errors"
	"testing"
)

type stubCompleter struct{}

func (stubCompleter) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	return nil, errors.New("not used in this test")
}

func (stubCompleter) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	return nil, errors.New("not used in this test")
}

func TestCompleterRoundTrip(t *testing.T) {
	var c LLMCompleter = stubCompleter{}
	ctx := WithCompleter(context.Background(), c)
	got, ok := CompleterFromContext(ctx)
	if !ok {
		t.Fatal("expected completer present in context")
	}
	if got == nil {
		t.Fatal("expected non-nil completer")
	}
}

func TestCompleterFromContextMissing(t *testing.T) {
	got, ok := CompleterFromContext(context.Background())
	if ok {
		t.Fatal("expected no completer in empty context")
	}
	if got != nil {
		t.Errorf("expected nil completer, got %v", got)
	}
}

func TestCompleterFromContextWrongType(t *testing.T) {
	// 极端情况：同 key 存了非接口实现值，必须安全返回 (nil, false)。
	ctx := context.WithValue(context.Background(), completerCtxKey{}, "not a completer")
	got, ok := CompleterFromContext(ctx)
	if ok || got != nil {
		t.Errorf("expected (nil, false), got (%v, %v)", got, ok)
	}
}

func TestCompleterSurvivesCancel(t *testing.T) {
	// 极端情况：cancel 后的 ctx 仍能读出注入的 completer。
	var c LLMCompleter = stubCompleter{}
	ctx, cancel := context.WithCancel(WithCompleter(context.Background(), c))
	cancel()
	if _, ok := CompleterFromContext(ctx); !ok {
		t.Error("expected completer still readable after cancel")
	}
}

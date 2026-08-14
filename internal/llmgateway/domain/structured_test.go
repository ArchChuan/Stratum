package domain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// testFieldErr 实现 FieldError：验证内核按字段名做白名单摘要、不泄露违规值。
type testFieldErr struct{ field, value string }

func (e *testFieldErr) Error() string { return "field " + e.field + " got " + e.value }
func (e *testFieldErr) Field() string { return e.field }

// captureLLMStub 记录每次请求的消息副本与 response_format，逐次返回 contents[i]。
type captureLLMStub struct {
	mu       sync.Mutex
	contents []string
	requests [][]Message
	formats  []*ResponseFormat
}

func (s *captureLLMStub) Complete(_ context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, append([]Message(nil), req.Messages...))
	s.formats = append(s.formats, req.ResponseFormat)
	idx := len(s.requests) - 1
	if idx >= len(s.contents) {
		idx = len(s.contents) - 1
	}
	return &CompletionResponse{Content: s.contents[idx]}, nil
}

// errLLMStub 每次调用恒返回 err。
type errLLMStub struct{ err error }

func (s *errLLMStub) Complete(context.Context, *CompletionRequest) (*CompletionResponse, error) {
	return nil, s.err
}

func TestStructuredRetryLoopRetriesWithCorrection(t *testing.T) {
	llm := &captureLLMStub{contents: []string{"bad", "good"}}
	_, err := StructuredRetryLoop(context.Background(), llm,
		&CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
		2, "extract_facts", func(content string) error {
			if content != "good" {
				return &testFieldErr{field: "importance", value: "1.5"}
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("calls = %d, want 2 (initial + correction retry)", len(llm.requests))
	}
	last := llm.requests[1]
	if len(last) != 2 || last[1].Role != "system" {
		t.Fatalf("retry must append system-role correction, got %#v", last)
	}
	if !strings.Contains(last[1].Content, "{correction: ") {
		t.Fatalf("correction must carry error context, got %q", last[1].Content)
	}
}

func TestStructuredRetryLoopExhaustsWithWhitelist(t *testing.T) {
	const secret = "hunter2-secret"
	llm := &captureLLMStub{contents: []string{"always-bad"}}
	_, err := StructuredRetryLoop(context.Background(), llm,
		&CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
		2, "extract_facts", func(string) error {
			return &testFieldErr{field: "fact_type", value: secret}
		})
	if !errors.Is(err, ErrStructuredOutputFailed) {
		t.Fatalf("err = %v, want ErrStructuredOutputFailed", err)
	}
	var soe *StructuredOutputError
	if !errors.As(err, &soe) {
		t.Fatalf("err = %T, want *StructuredOutputError", err)
	}
	if soe.Summary.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (1 initial + 2 retries)", soe.Summary.Attempts)
	}
	msg := err.Error()
	for _, want := range []string{"attempts=3", "invalid_fields=fact_type"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("err %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, secret) {
		t.Fatalf("typed error must not leak invalid value, got %q", msg)
	}
}

func TestStructuredRetryLoopFailFastOnProviderError(t *testing.T) {
	llm := &errLLMStub{err: errors.New("upstream 500")}
	_, err := StructuredRetryLoop(context.Background(), llm,
		&CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
		2, "extract_facts", func(string) error { return nil })
	if err == nil {
		t.Fatal("provider error must propagate")
	}
	if !strings.Contains(err.Error(), "llm complete") {
		t.Fatalf("error must be wrapped with stage, got %v", err)
	}
	if errors.Is(err, ErrStructuredOutputFailed) {
		t.Fatalf("provider hard error must not be ErrStructuredOutputFailed: %v", err)
	}
}

func TestStructuredRetryLoopSetsJSONObjectOnCloneOnly(t *testing.T) {
	llm := &captureLLMStub{contents: []string{"ok"}}
	callerReq := &CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}}
	if _, err := StructuredRetryLoop(context.Background(), llm, callerReq, 0, "kind",
		func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(llm.formats) != 1 || llm.formats[0] == nil || llm.formats[0].Type != "json_object" {
		t.Fatalf("kernel must set json_object on the request it sends, got %#v", llm.formats)
	}
	if callerReq.ResponseFormat != nil {
		t.Fatalf("kernel must not mutate caller request (clone semantics), got %#v", callerReq.ResponseFormat)
	}
}

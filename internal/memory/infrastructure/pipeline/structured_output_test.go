package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// seqLLMStub 逐次返回 contents[i]，耗尽后复用最后一个；记录每次请求的
// Messages 副本，用于断言带错 correction 已作为 system role 附加。
type seqLLMStub struct {
	mu       sync.Mutex
	contents []string
	requests [][]memport.CompletionMessage
}

func (s *seqLLMStub) Complete(_ context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, append([]memport.CompletionMessage(nil), req.Messages...))
	idx := len(s.requests) - 1
	if idx >= len(s.contents) {
		idx = len(s.contents) - 1
	}
	return &memport.CompletionResponse{Content: s.contents[idx]}, nil
}

// errLLMStub 每次调用恒返回 err。
type errLLMStub struct{ err error }

func (s *errLLMStub) Complete(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	return nil, s.err
}

// validateFactList 逐条校验事实，命中第一条错误即返回（语义校验）。
func validateFactList(facts []*memport.ExtractedFact) error {
	for _, f := range facts {
		if err := f.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func logText(entries []observer.LoggedEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Message)
		sb.WriteString(" ")
		sb.WriteString(fmt.Sprintf("%v", e.ContextMap()))
	}
	return sb.String()
}

// TestCompleteStructuredRetriesWithCorrection 验证用户约束核心：校验失败时
// 不是简单重试，而是把具体字段/值/原因构造成 system-role correction 丢回模型。
func TestCompleteStructuredRetriesWithCorrection(t *testing.T) {
	llm := &seqLLMStub{contents: []string{
		`[{"content":"a","importance":1.5,"fact_type":"skill"}]`, // importance 越界
		`[{"content":"a","importance":0.8,"fact_type":"skill"}]`, // 修复后合法
	}}
	_, err := CompleteStructured(context.Background(), llm,
		&memport.CompletionRequest{Messages: []memport.CompletionMessage{{Role: "user", Content: "x"}}},
		parseExtractedFacts, validateFactList, nil, "extract_facts")
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
	if !strings.Contains(last[1].Content, "importance") {
		t.Fatalf("correction must name the field, got %q", last[1].Content)
	}
}

// TestCompleteStructuredFailFastOnProviderError 验证 provider 硬错误不消耗重试：
// fail-fast，错误向上传播，不落到 typed error。
func TestCompleteStructuredFailFastOnProviderError(t *testing.T) {
	llm := &errLLMStub{err: errors.New("upstream 500")}
	_, err := CompleteStructured(context.Background(), llm,
		&memport.CompletionRequest{Messages: []memport.CompletionMessage{{Role: "user", Content: "x"}}},
		parseExtractedFacts, validateFactList, nil, "extract_facts")
	if err == nil {
		t.Fatal("provider error must propagate")
	}
	if !strings.Contains(err.Error(), "llm complete") {
		t.Fatalf("error must be wrapped with stage, got %v", err)
	}
	if errors.Is(err, ErrStructuredExtractionFailed) {
		t.Fatalf("provider hard error must not be ErrStructuredExtractionFailed: %v", err)
	}
}

// TestCompleteStructuredTypedErrorAfterRetries 验证 0 条通过 → typed error
// （调用方保留 MarkFailed/DLQ 语义），且耗尽错误不含违规值（防 PII）。
func TestCompleteStructuredTypedErrorAfterRetries(t *testing.T) {
	const badType = "hunter2-secret" // 语义上含敏感信息：坏枚举值
	llm := &seqLLMStub{contents: []string{
		`[{"content":"a","importance":0.5,"fact_type":"` + badType + `"}]`,
	}}
	_, err := CompleteStructured(context.Background(), llm,
		&memport.CompletionRequest{Messages: []memport.CompletionMessage{{Role: "user", Content: "x"}}},
		parseExtractedFacts, validateFactList, nil, "extract_facts")
	if !errors.Is(err, ErrStructuredExtractionFailed) {
		t.Fatalf("err = %v, want ErrStructuredExtractionFailed", err)
	}
	msg := err.Error()
	for _, want := range []string{"attempts=3", "invalid_fields=fact_type"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("err %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, badType) {
		t.Fatalf("typed error must not leak invalid value, got %q", msg)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("calls = %d, want 3 (1 initial + %d retries)", len(llm.requests), 2)
	}
}

// TestExtractFactsPartialSuccess 验证部分成功语义：≥1 条通过立即返回通过子集，
// 不为单条坏事实浪费重试；0 条通过才报错。
func TestExtractFactsPartialSuccess(t *testing.T) {
	llm := &seqLLMStub{contents: []string{
		`[{"content":"good","importance":0.7,"fact_type":"state"},
		  {"content":"","importance":0.7,"fact_type":"state"}]`, // 第 2 条 content 空
	}}
	facts, err := NewLLMExtractor(llm).ExtractFacts(context.Background(), "user-1", "agent-1", "msg")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Content != "good" {
		t.Fatalf("partial success must return the valid subset, got %#v", facts)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("partial success must not retry, calls = %d", len(llm.requests))
	}
}

// TestExtractFactsEmptyArrayIsValid 验证模型返回 []（明确无事实）是合法结果，
// 非校验失败、不触发重试（避免把「无事实」误判为失败走 DLQ）。
func TestExtractFactsEmptyArrayIsValid(t *testing.T) {
	llm := &seqLLMStub{contents: []string{"[]"}}
	facts, err := NewLLMExtractor(llm).ExtractFacts(context.Background(), "user-1", "agent-1", "msg")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("empty array must yield empty result, got %#v", facts)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("empty array must not retry, calls = %d", len(llm.requests))
	}
}

// TestCompleteStructuredPIIWhitelistLogging 验证降级日志只记白名单字段
// （字段名 + 计数），禁止原始模型输出、违规值与原文。
func TestCompleteStructuredPIIWhitelistLogging(t *testing.T) {
	const secret = "hunter2-token-abc123"
	llm := &seqLLMStub{contents: []string{
		`[{"content":"` + secret + `","importance":0.5,"fact_type":"` + secret + `"}]`,
	}}
	core, logs := observer.New(zapcore.WarnLevel)
	_, err := CompleteStructured(context.Background(), llm,
		&memport.CompletionRequest{Messages: []memport.CompletionMessage{{Role: "user", Content: secret}}},
		parseExtractedFacts, validateFactList, zap.New(core), "extract_facts")
	if !errors.Is(err, ErrStructuredExtractionFailed) {
		t.Fatalf("err = %v, want ErrStructuredExtractionFailed", err)
	}
	if logs.FilterMessage("memory.structured.degraded").Len() != 1 {
		t.Fatalf("degraded WARN must be emitted once, got %d", logs.FilterMessage("memory.structured.degraded").Len())
	}
	text := logText(logs.All())
	if strings.Contains(text, secret) {
		t.Fatalf("degraded log leaks sensitive value: %q", text)
	}
	if !strings.Contains(text, "invalid_field_fact_type") {
		t.Fatalf("degraded log must carry field name (whitelist), got %q", text)
	}
}

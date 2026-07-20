package workers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

// fakeLLMClient is a minimal pipeline.LLMClient for profiler tests.
type fakeLLMClient struct {
	resp        string
	err         error
	lastPrompt  string
	lastMaxToks int
}

func (f *fakeLLMClient) Complete(_ context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	if len(req.Messages) > 0 {
		f.lastPrompt = req.Messages[0].Content
	}
	f.lastMaxToks = req.MaxTokens
	if f.err != nil {
		return nil, f.err
	}
	return &memport.CompletionResponse{Content: f.resp}, nil
}

func TestLLMEntityProfiler_EmptyFactsSkips(t *testing.T) {
	client := &fakeLLMClient{resp: "should not be used"}
	p := workers.NewLLMEntityProfiler(client)

	profile, err := p.GenerateProfile(context.Background(), "Alice", "person", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "" {
		t.Fatalf("expected empty profile for no facts, got %q", profile)
	}
	if client.lastPrompt != "" {
		t.Fatal("LLM must not be called when there are no facts")
	}
}

func TestLLMEntityProfiler_GeneratesTrimmedProfile(t *testing.T) {
	client := &fakeLLMClient{resp: "  Alice 偏好深色模式，常在夜间工作。  \n"}
	p := workers.NewLLMEntityProfiler(client)

	profile, err := p.GenerateProfile(context.Background(), "Alice", "person",
		[]string{"用户偏好深色模式", "用户常在夜间工作"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "Alice 偏好深色模式，常在夜间工作。" {
		t.Fatalf("profile not trimmed as expected: %q", profile)
	}
	// Prompt must carry entity identity and every fact so the LLM has full context.
	for _, want := range []string{"Alice", "person", "用户偏好深色模式", "用户常在夜间工作"} {
		if !strings.Contains(client.lastPrompt, want) {
			t.Fatalf("prompt missing %q; got:\n%s", want, client.lastPrompt)
		}
	}
}

func TestLLMEntityProfiler_PropagatesError(t *testing.T) {
	client := &fakeLLMClient{err: errors.New("llm down")}
	p := workers.NewLLMEntityProfiler(client)

	_, err := p.GenerateProfile(context.Background(), "Alice", "person", []string{"fact"})
	if err == nil {
		t.Fatal("expected error to propagate from LLM client")
	}
}

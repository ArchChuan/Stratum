package pipeline

import (
	"context"
	"strings"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type extractorLLMStub struct {
	content string
	prompt  string
	model   string
}

func (s *extractorLLMStub) Complete(_ context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	s.prompt = req.Messages[0].Content
	s.model = req.Model
	return &memport.CompletionResponse{Content: s.content}, nil
}

func TestLLMExtractorDecodesFactTypeAndExplicitZeroConfidence(t *testing.T) {
	llm := &extractorLLMStub{content: `[{"content":"uses Go","importance":0.8,"fact_type":"skill","confidence":0.0,"entities":["Go"]}]`}
	facts, err := NewLLMExtractor(llm).ExtractFacts(context.Background(), "user-1", "agent-1", "I use Go")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].FactType != "skill" || facts[0].Confidence == nil || *facts[0].Confidence != 0 {
		t.Fatalf("unexpected decoded fact: %#v", facts)
	}
	if !strings.Contains(llm.prompt, `"confidence":0.0-1.0`) {
		t.Fatal("extractor prompt must request confidence in pipeline JSON")
	}
}

// TestLLMExtractorUsesFallbackSystemPrompt 验证抽取模板走内置常量（mechanism
// 移除后为唯一权威），占位照常渲染。
func TestLLMExtractorUsesFallbackSystemPrompt(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "长期记忆提取助手") {
		t.Fatalf("fallback prompt missing: %q", llm.prompt)
	}
}

// TestLLMExtractorLeavesModelEmpty 验证抽取请求 Model 为空（llmgateway client
// 默认解析，pre-refactor 行为；金丝雀回归）。
func TestLLMExtractorLeavesModelEmpty(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if llm.model != "" {
		t.Fatalf("expected empty model by default, got %q", llm.model)
	}
}

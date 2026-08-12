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
}

func (s *extractorLLMStub) Complete(_ context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	s.prompt = req.Messages[0].Content
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

// TestLLMExtractorUsesInjectedSystemPromptOverFallback 验证机制基线抽取模板
// 注入优先、空值回退内置常量（现状行为）；注入模板的占位照常渲染。
func TestLLMExtractorUsesInjectedSystemPromptOverFallback(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "长期记忆提取助手") {
		t.Fatalf("fallback prompt missing: %q", llm.prompt)
	}

	extractor.SetSystemPrompt("模板：用户 %s 助手 %s 上限 %d")
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "模板：用户 user-1 助手 agent-1") {
		t.Fatalf("injected prompt not used: %q", llm.prompt)
	}
}

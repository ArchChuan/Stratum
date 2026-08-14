package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"

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

// TestLLMExtractorUsesBaselineExtractionModel 验证抽取请求显式携带机制基线
// 的 ExtractionModel（profile 解析的唯一落点）。
func TestLLMExtractorUsesBaselineExtractionModel(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)

	// 未注入 model：请求 Model 为空，走客户端默认解析（改造前行为）。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if llm.model != "" {
		t.Fatalf("expected empty model by default, got %q", llm.model)
	}

	extractor.SetExtractionModel("qwen-plus")
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if llm.model != "qwen-plus" {
		t.Fatalf("expected request Model %q, got %q", "qwen-plus", llm.model)
	}
}

// extractorResolverStub is a scripted ResourceParamResolver double keyed by
// agentID; err short-circuits every resolve to exercise the degrade path.
type extractorResolverStub struct {
	perAgent map[string]any
	err      error
}

func (s extractorResolverStub) Resolve(_ context.Context, _ string, agentID, _ string) (any, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	v, ok := s.perAgent[agentID]
	return v, ok, nil
}

// TestLLMExtractorMaxFactsPerAgent 验证 memory.max_facts_per_extraction 按 agent
// 解析注入抽取提示词:命中 agent 记录用其值,未命中回落常量默认。
func TestLLMExtractorMaxFactsPerAgent(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)
	extractor.SetSystemPrompt("模板：用户 %s 助手 %s 上限 %d")
	extractor.SetTenantID("t1")
	extractor.SetResourceResolver(extractorResolverStub{perAgent: map[string]any{"agent-1": float64(35)}})

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "上限 35") {
		t.Fatalf("per-agent max_facts not applied: %q", llm.prompt)
	}

	// 未记录的 agent → 回落常量默认。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-other", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("上限 %d", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("fallback default not applied: %q", llm.prompt)
	}
}

// TestLLMExtractorMaxFactsDegrade 验证 resolver 缺省/报错时 maxFacts 回落常量
// 默认,不阻塞抽取。
func TestLLMExtractorMaxFactsDegrade(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)
	extractor.SetSystemPrompt("用户 %s 助手 %s 上限 %d")
	extractor.SetTenantID("t1")

	// 未接 resolver → 常量默认。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("上限 %d", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("nil resolver must fall back to default: %q", llm.prompt)
	}

	// resolver 报错 → 常量默认。
	extractor.SetResourceResolver(extractorResolverStub{err: errors.New("db down")})
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("上限 %d", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("resolver error must fall back to default: %q", llm.prompt)
	}
}

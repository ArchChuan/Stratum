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
	extractor.SetTenantID("t1")
	extractor.SetResourceResolver(extractorResolverStub{perAgent: map[string]any{"agent-1": float64(35)}})

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "最多提取 35 条事实") {
		t.Fatalf("per-agent max_facts not applied: %q", llm.prompt)
	}

	// 未记录的 agent → 回落常量默认。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-other", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("最多提取 %d 条事实", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("fallback default not applied: %q", llm.prompt)
	}
}

// TestLLMExtractorMaxFactsDegrade 验证 resolver 缺省/报错时 maxFacts 回落常量
// 默认,不阻塞抽取。
func TestLLMExtractorMaxFactsDegrade(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)
	extractor.SetTenantID("t1")

	// 未接 resolver → 常量默认。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("最多提取 %d 条事实", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("nil resolver must fall back to default: %q", llm.prompt)
	}

	// resolver 报错 → 常量默认。
	extractor.SetResourceResolver(extractorResolverStub{err: errors.New("db down")})
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, fmt.Sprintf("最多提取 %d 条事实", constants.MemoryMaxFactsPerExtraction)) {
		t.Fatalf("resolver error must fall back to default: %q", llm.prompt)
	}
}

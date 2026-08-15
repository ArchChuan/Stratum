package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type extractorLLMStub struct {
	content string
	prompt  string
	model   string
}

func (s *extractorLLMStub) Complete(_ context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	s.prompt = req.Messages[0].Content
	s.model = req.Model
	return &llmdomain.CompletionResponse{Content: s.content}, nil
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

// keyedResolverStub 按 (agentID, key) 返回配置值,用于验证 extraction_prompt/model
// 独立解析(区别于 max_facts 的 agent-only 桩——后者无法区分 key)。
type keyedResolverStub struct {
	vals map[string]any // "agentID:key" → value
	err  error
}

func (s keyedResolverStub) Resolve(_ context.Context, _ string, agentID, key string) (any, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	v, ok := s.vals[agentID+":"+key]
	return v, ok, nil
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

// TestLLMExtractorCustomExtractionPrompt 验证 memory.extraction_prompt 按 agent
// 解析:非空自定义 prompt 按 %s(userID)/%s(agentID)/%d(maxFacts) 插值注入;缺失
// 回落内置模板。
func TestLLMExtractorCustomExtractionPrompt(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)
	extractor.SetTenantID("t1")
	extractor.SetResourceResolver(keyedResolverStub{vals: map[string]any{
		"agent-1:memory.extraction_prompt": "fact machine for %s / %s / %d",
	}})

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("fact machine for user-1 / agent-1 / %d", constants.MemoryMaxFactsPerExtraction)
	if llm.prompt != want {
		t.Fatalf("custom prompt not interpolated: got %q want %q", llm.prompt, want)
	}

	// 未记录 agent → 内置模板。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-other", "msg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "长期记忆提取助手") {
		t.Fatalf("missing agent must fall back to builtin: %q", llm.prompt)
	}
}

// TestLLMExtractorCustomExtractionModel 验证 memory.extraction_model 非空时传入
// 请求;缺失回落空串(client 默认解析)。
func TestLLMExtractorCustomExtractionModel(t *testing.T) {
	llm := &extractorLLMStub{content: `[]`}
	extractor := NewLLMExtractor(llm)
	extractor.SetTenantID("t1")
	extractor.SetResourceResolver(keyedResolverStub{vals: map[string]any{
		"agent-1:memory.extraction_model": "qwen-turbo",
	}})

	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-1", "msg"); err != nil {
		t.Fatal(err)
	}
	if llm.model != "qwen-turbo" {
		t.Fatalf("custom model not applied: got %q", llm.model)
	}

	// 未记录 agent → 空模型(默认解析)。
	if _, err := extractor.ExtractFacts(context.Background(), "user-1", "agent-other", "msg"); err != nil {
		t.Fatal(err)
	}
	if llm.model != "" {
		t.Fatalf("missing model must fall back to empty: got %q", llm.model)
	}
}

package pipeline

import (
	"context"
	"strings"
	"testing"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type extractorLLMStub struct {
	content string
	prompt  string
}

func (s *extractorLLMStub) Complete(_ context.Context, req *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error) {
	s.prompt = req.Messages[0].Content
	return &llmgateway.CompletionResponse{Content: s.content}, nil
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

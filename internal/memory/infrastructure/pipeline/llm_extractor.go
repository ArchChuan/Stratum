package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// LLMExtractor adapts LLMClient to memport.LLMExtractor.
type LLMExtractor struct{ client LLMClient }

func NewLLMExtractor(client LLMClient) *LLMExtractor {
	return &LLMExtractor{client: client}
}

func (e *LLMExtractor) ExtractFacts(ctx context.Context, _, _ string, message string) ([]*memport.ExtractedFact, error) {
	const system = `Extract factual statements from the conversation. Return a JSON array only:
[{"content":"...","importance":0.7,"entities":["name"]}]`
	resp, err := e.client.Complete(ctx, &llmgateway.CompletionRequest{
		Messages: []llmgateway.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: message},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("llm extract: %w", err)
	}
	raw := resp.Content
	start := strings.Index(raw, "[")
	if start == -1 {
		return nil, fmt.Errorf("parse extracted facts: no JSON array in response")
	}
	var facts []*memport.ExtractedFact
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&facts); err != nil {
		return nil, fmt.Errorf("parse extracted facts: %w", err)
	}
	return facts, nil
}

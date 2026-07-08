package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
		MaxTokens: constants.MemoryExtractLLMMaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("llm extract: %w", err)
	}
	raw := resp.Content
	start := strings.Index(raw, "[")
	if start == -1 {
		return nil, fmt.Errorf("parse extracted facts: no JSON array in response")
	}
	body := raw[start:]
	var facts []*memport.ExtractedFact
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&facts); err != nil {
		// Token limit may have truncated the JSON mid-object; recover by closing at the last complete item.
		if recovered := recoverTruncatedArray(body); recovered != "" {
			if err2 := json.Unmarshal([]byte(recovered), &facts); err2 == nil {
				return facts, nil
			}
		}
		return nil, fmt.Errorf("parse extracted facts: %w", err)
	}
	return facts, nil
}

// recoverTruncatedArray finds the last complete JSON object in a truncated array and closes it.
func recoverTruncatedArray(s string) string {
	last := strings.LastIndex(s, "},")
	if last == -1 {
		last = strings.LastIndex(s, "}")
	} else {
		last++ // include the }
	}
	if last == -1 {
		return ""
	}
	return s[:last+1] + "]"
}

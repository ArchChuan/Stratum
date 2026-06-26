package workers

import (
	"context"
	"encoding/json"
	"fmt"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
)

// LLMSuperseder adapts pipeline.LLMClient to memport.LLMSuperseder.
type LLMSuperseder struct{ client pipeline.LLMClient }

func NewLLMSuperseder(client pipeline.LLMClient) *LLMSuperseder {
	return &LLMSuperseder{client: client}
}

func (s *LLMSuperseder) JudgeSupersede(ctx context.Context, oldFact, newFact string) (*memport.SupersedeJudgment, error) {
	prompt := fmt.Sprintf("Does this new fact supersede the old?\nOld: %s\nNew: %s\nReturn JSON only: {\"supersedes\":true,\"reason\":\"...\"}", oldFact, newFact)
	resp, err := s.client.Complete(ctx, &llmgateway.CompletionRequest{
		Messages:  []llmgateway.Message{{Role: "user", Content: prompt}},
		MaxTokens: 256,
	})
	if err != nil {
		return nil, fmt.Errorf("llm supersede: %w", err)
	}
	var j memport.SupersedeJudgment
	if err := json.Unmarshal([]byte(resp.Content), &j); err != nil {
		return nil, fmt.Errorf("parse judgment: %w", err)
	}
	return &j, nil
}

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
	prompt := fmt.Sprintf(`判断新事实是否应该取代旧事实。

旧事实：%s
新事实：%s

判断标准：
- 如果新事实是对旧事实的更新、纠正或推翻，则应取代（supersedes: true）
- 如果两者描述不同方面或可以并存，则不取代（supersedes: false）
- 如果新事实只是旧事实的子集或更模糊的表达，则不取代

只输出 JSON，不加任何说明：
{"supersedes": true/false, "reason": "简短说明"}`, oldFact, newFact)
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

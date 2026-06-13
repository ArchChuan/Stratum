// Package skill provides skill execution framework.
package skill

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"go.uber.org/zap"
)

type LLMSkill struct {
	*BaseSkill
	SystemPrompt string
	Model        string
	Temperature  float32
	MaxTokens    int
	gateway      *llmgateway.Gateway
	logger       *zap.Logger
}

func NewLLMSkill(id, name, description, systemPrompt, model string, temperature float32, maxTokens int, gateway *llmgateway.Gateway, logger *zap.Logger) *LLMSkill {
	return &LLMSkill{
		BaseSkill: &BaseSkill{
			ID:          id,
			Name:        name,
			Description: description,
			Type:        "llm",
		},
		SystemPrompt: systemPrompt,
		Model:        model,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
		gateway:      gateway,
		logger:       logger,
	}
}

func (ls *LLMSkill) GetConfig() map[string]any {
	return map[string]any{
		"system_prompt": ls.SystemPrompt,
		"model":         ls.Model,
		"temperature":   ls.Temperature,
		"max_tokens":    ls.MaxTokens,
	}
}

func (ls *LLMSkill) Execute(ctx context.Context, input interface{}) (interface{}, error) {
	gw := ls.gateway
	if override, ok := llmgateway.GatewayFromContext(ctx); ok {
		gw = override
	}

	inputMap, _ := input.(map[string]interface{})

	// model: stored config is default, input can override
	model := ls.Model
	if m, ok := inputMap["model"].(string); ok && m != "" {
		model = m
	}
	if model == "" {
		return nil, fmt.Errorf("model not specified")
	}

	prompt, ok := inputMap["prompt"].(string)
	if !ok || prompt == "" {
		return nil, fmt.Errorf("prompt not specified")
	}

	req := &llmgateway.CompletionRequest{
		Model:    model,
		Messages: []llmgateway.Message{},
	}
	if ls.SystemPrompt != "" {
		req.Messages = append(req.Messages, llmgateway.Message{Role: "system", Content: ls.SystemPrompt})
	}
	req.Messages = append(req.Messages, llmgateway.Message{Role: "user", Content: prompt})

	req.Temperature = ls.Temperature
	if t, ok := inputMap["temperature"].(float32); ok {
		req.Temperature = t
	}

	req.MaxTokens = ls.MaxTokens
	if m, ok := inputMap["max_tokens"].(int); ok && m > 0 {
		req.MaxTokens = m
	}

	resp, err := gw.Complete(ctx, req)
	if err != nil {
		ls.logger.Error("LLM call failed", zap.Error(err))
		return nil, err
	}

	ls.logger.Info("LLM call success", zap.String("model", model), zap.Int("tokens", resp.Usage.TotalTokens))

	return map[string]interface{}{
		"content": resp.Content,
		"model":   resp.Model,
		"usage": map[string]int{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}, nil
}

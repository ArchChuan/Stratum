// Package llmgateway provides LLM gateway abstraction.
package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"
)

type OpenAIClient struct {
	apiKey   string
	endpoint string
	logger   *zap.Logger
	client   *http.Client
}

func NewOpenAIClient(apiKey, endpoint string, logger *zap.Logger) *OpenAIClient {
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	return &OpenAIClient{
		apiKey:   apiKey,
		endpoint: endpoint,
		logger:   logger,
		client:   &http.Client{},
	}
}

func (c *OpenAIClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	openaiReq := map[string]interface{}{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"top_p":       req.TopP,
	}

	body, err := json.Marshal(openaiReq)
	if err != nil {
		c.logger.Error("failed to marshal request", zap.Error(err))
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		c.logger.Error("failed to create request", zap.Error(err))
		return nil, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		c.logger.Error("failed to call OpenAI API", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.logger.Error("OpenAI API error", zap.Int("status", resp.StatusCode), zap.String("body", string(body)))
		return nil, fmt.Errorf("OpenAI API error: %d", resp.StatusCode)
	}

	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		c.logger.Error("failed to decode response", zap.Error(err))
		return nil, err
	}

	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	result := &CompletionResponse{
		Content: openaiResp.Choices[0].Message.Content,
		Model:   req.Model,
	}
	result.Usage.PromptTokens = openaiResp.Usage.PromptTokens
	result.Usage.CompletionTokens = openaiResp.Usage.CompletionTokens
	result.Usage.TotalTokens = openaiResp.Usage.TotalTokens

	c.logger.Info("OpenAI completion success", zap.String("model", req.Model), zap.Int("tokens", result.Usage.TotalTokens))
	return result, nil
}

func (c *OpenAIClient) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/models", nil)
	if err != nil {
		return err
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenAI health check failed: %d", resp.StatusCode)
	}

	return nil
}

func (c *OpenAIClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	openaiReq := map[string]interface{}{
		"input": req.Input,
		"model": model,
	}

	body, err := json.Marshal(openaiReq)
	if err != nil {
		c.logger.Error("failed to marshal embedding request", zap.Error(err))
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		c.logger.Error("failed to create embedding request", zap.Error(err))
		return nil, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		c.logger.Error("failed to call OpenAI embeddings API", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		c.logger.Error("OpenAI embeddings API error",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)))
		return nil, fmt.Errorf("OpenAI embeddings API error: %d", resp.StatusCode)
	}

	var openaiResp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		c.logger.Error("failed to decode embedding response", zap.Error(err))
		return nil, err
	}

	result := &EmbeddingResponse{
		Embeddings: make([][]float32, len(openaiResp.Data)),
	}
	for i, d := range openaiResp.Data {
		result.Embeddings[i] = d.Embedding
	}

	c.logger.Info("OpenAI embeddings success",
		zap.String("model", model),
		zap.Int("count", len(result.Embeddings)))
	return result, nil
}

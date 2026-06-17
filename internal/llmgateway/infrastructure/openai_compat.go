package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// ProviderConfig holds the minimal configuration that differentiates one
// OpenAI-compatible provider from another.
type ProviderConfig struct {
	Name        string
	BaseURL     string
	APIKey      string
	HealthModel string
	Models      []string
}

// OpenAICompatClient implements LLMClient, StreamingLLMClient, and
// EmbeddingClient for any provider that speaks the OpenAI-compatible protocol.
type OpenAICompatClient struct {
	cfg    ProviderConfig
	http   *http.Client
	logger *zap.Logger
}

func NewOpenAICompatClient(cfg ProviderConfig, logger *zap.Logger) *OpenAICompatClient {
	return &OpenAICompatClient{
		cfg:    cfg,
		http:   &http.Client{Timeout: constants.LLMRequestTimeout},
		logger: logger,
	}
}

func (c *OpenAICompatClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.cfg.Name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", c.cfg.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Error(c.cfg.Name+": http error",
			zap.String("model", req.Model),
			zap.Int("status", resp.StatusCode),
		)
		return nil, fmt.Errorf("%s: status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
	}

	var out openAICompletionResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", c.cfg.Name, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s: no choices in response", c.cfg.Name)
	}

	return &CompletionResponse{
		Content:   out.Choices[0].Message.Content,
		Model:     out.Model,
		ToolCalls: out.Choices[0].Message.ToolCalls,
		Usage:     out.Usage,
	}, nil
}

func (c *OpenAICompatClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	streamReq := *req
	streamReq.Stream = true
	body, err := json.Marshal(streamReq)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal stream request: %w", c.cfg.Name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	streamClient := &http.Client{}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do stream request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: stream status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
	}

	var result CompletionResponse
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if chunk.Usage != nil {
			result.Usage.PromptTokens = chunk.Usage.PromptTokens
			result.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			result.Usage.TotalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) > 0 {
			if t := chunk.Choices[0].Delta.Content; t != "" {
				result.Content += t
				onToken(t)
			}
			if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
				result.ToolCalls = chunk.Choices[0].Delta.ToolCalls
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", c.cfg.Name, err)
	}
	return &result, nil
}

func (c *OpenAICompatClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	body, err := json.Marshal(map[string]any{
		"model": req.Model,
		"input": req.Input,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: marshal embed request: %w", c.cfg.Name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build embed request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do embed request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read embed body: %w", c.cfg.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Error(c.cfg.Name+": embed http error",
			zap.String("model", req.Model),
			zap.Int("status", resp.StatusCode),
		)
		return nil, fmt.Errorf("%s: embed status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
	}

	var out openAIEmbedResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode embed response: %w", c.cfg.Name, err)
	}

	embeddings := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		embeddings[i] = d.Embedding
	}
	return &EmbeddingResponse{Embeddings: embeddings}, nil
}

func (c *OpenAICompatClient) Health(ctx context.Context) error {
	_, err := c.Complete(ctx, &CompletionRequest{
		Model:     c.cfg.HealthModel,
		Messages:  []Message{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	return err
}

func (c *OpenAICompatClient) Models() []string {
	return c.cfg.Models
}

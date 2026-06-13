package llmgateway

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

const qwenBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

type QwenClient struct {
	apiKey string
	base   string
	http   *http.Client
	logger *zap.Logger
}

func NewQwenClient(apiKey string, logger *zap.Logger) *QwenClient {
	return &QwenClient{
		apiKey: apiKey,
		base:   qwenBaseURL,
		http:   &http.Client{Timeout: constants.LLMRequestTimeout},
		logger: logger,
	}
}

func NewQwenClientWithBase(apiKey, baseURL string, logger *zap.Logger) *QwenClient {
	return &QwenClient{
		apiKey: apiKey,
		base:   baseURL,
		http:   &http.Client{Timeout: constants.LLMRequestTimeout},
		logger: logger,
	}
}

func (c *QwenClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("qwen: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qwen: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("qwen: do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qwen: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Error("qwen: http error",
			zap.String("model", req.Model),
			zap.Int("status", resp.StatusCode),
		)
		return nil, fmt.Errorf("qwen: status %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("qwen: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("qwen: no choices in response")
	}

	return &CompletionResponse{
		Content:   out.Choices[0].Message.Content,
		Model:     out.Model,
		ToolCalls: out.Choices[0].Message.ToolCalls,
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		},
	}, nil
}

// CompleteStream calls the Qwen streaming API (OpenAI-compatible SSE format).
func (c *QwenClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	streamReq := *req
	streamReq.Stream = true
	body, err := json.Marshal(streamReq)
	if err != nil {
		return nil, fmt.Errorf("qwen: marshal stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qwen: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	streamClient := &http.Client{}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("qwen: do stream request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qwen: stream status %d: %s", resp.StatusCode, string(raw))
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
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string     `json:"content"`
					ToolCalls []ToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Model string `json:"model"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
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
		return nil, fmt.Errorf("qwen: read stream: %w", err)
	}
	return &result, nil
}

func (c *QwenClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	body, err := json.Marshal(map[string]any{
		"model": req.Model,
		"input": req.Input,
	})
	if err != nil {
		return nil, fmt.Errorf("qwen: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qwen: build embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("qwen: do embed request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qwen: read embed body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Error("qwen: embed http error",
			zap.String("model", req.Model),
			zap.Int("status", resp.StatusCode),
		)
		return nil, fmt.Errorf("qwen: embed status %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("qwen: decode embed response: %w", err)
	}

	embeddings := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		embeddings[i] = d.Embedding
	}
	return &EmbeddingResponse{Embeddings: embeddings}, nil
}

func (c *QwenClient) Health(ctx context.Context) error {
	_, err := c.Complete(ctx, &CompletionRequest{
		Model:     "qwen-turbo",
		Messages:  []Message{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	return err
}

func (c *QwenClient) Models() []string {
	return []string{"qwen-turbo", "qwen-plus", "qwen-max", "qwen-long"}
}

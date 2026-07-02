package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
	cfg        ProviderConfig
	http       *http.Client // non-streaming: flat Timeout guards stuck complete calls
	streamHTTP *http.Client // streaming: transport-level timeouts only, no flat Timeout
	logger     *zap.Logger
}

func NewOpenAICompatClient(cfg ProviderConfig, logger *zap.Logger) *OpenAICompatClient {
	// Streaming client: transport timeouts protect dial/TLS/TTFT without
	// imposing a flat wall-clock cap that would kill legitimately slow models.
	streamTransport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	return &OpenAICompatClient{
		cfg:        cfg,
		http:       &http.Client{Timeout: constants.LLMRequestTimeout},
		streamHTTP: &http.Client{Transport: streamTransport},
		logger:     logger,
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

	// Idle-token watchdog: cancel the request if no token arrives for LLMStreamIdleTimeout.
	// Resets on every received token, so legitimately slow models are not penalised.
	// Catches mid-stream network stalls that the outer execCtx would only detect after 90s.
	idleCtx, idleCancel := context.WithCancelCause(ctx)
	defer idleCancel(nil)
	idleTimer := time.AfterFunc(constants.LLMStreamIdleTimeout, func() {
		idleCancel(fmt.Errorf("stream idle: no token received for %s", constants.LLMStreamIdleTimeout))
	})
	defer idleTimer.Stop()

	wrappedOnToken := func(token string) {
		idleTimer.Reset(constants.LLMStreamIdleTimeout)
		onToken(token)
	}

	httpReq, err := http.NewRequestWithContext(idleCtx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.streamHTTP.Do(httpReq)
	if err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
		return nil, fmt.Errorf("%s: do stream request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: stream status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
	}

	var result CompletionResponse
	tcAcc := make(map[int]*streamToolCallDelta)
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
				wrappedOnToken(t)
			}
			for _, d := range chunk.Choices[0].Delta.ToolCalls {
				acc, ok := tcAcc[d.Index]
				if !ok {
					acc = &streamToolCallDelta{Index: d.Index}
					tcAcc[d.Index] = acc
				}
				if d.ID != "" {
					acc.ID = d.ID
				}
				if d.Type != "" {
					acc.Type = d.Type
				}
				if d.Function.Name != "" {
					acc.Function.Name = d.Function.Name
				}
				acc.Function.Arguments += d.Function.Arguments
			}
		}
	}
	// convert accumulated deltas to ToolCall slice ordered by index
	result.ToolCalls = make([]ToolCall, len(tcAcc))
	for idx, acc := range tcAcc {
		if idx < len(result.ToolCalls) {
			result.ToolCalls[idx] = ToolCall{
				ID:   acc.ID,
				Type: acc.Type,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: acc.Function.Name, Arguments: acc.Function.Arguments},
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
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

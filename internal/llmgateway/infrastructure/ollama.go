package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Ollama native API types
// ---------------------------------------------------------------------------

// ollamaChatRequest is the JSON body for POST /api/chat.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Options  ollamaChatOptions   `json:"options,omitempty"`
	Tools    []ollamaToolDef     `json:"tools,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatOptions struct {
	Temperature float32 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaChatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool  `json:"done"`
	TotalDuration   int64 `json:"total_duration"`
	EvalCount       int   `json:"eval_count"`
	PromptEvalCount int   `json:"prompt_eval_count"`
}

type ollamaTagsResponse struct {
	Models []ollamaTagModel `json:"models"`
}

type ollamaTagModel struct {
	Name string `json:"name"`
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type ollamaToolDef struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ---------------------------------------------------------------------------
// OllamaClient
// ---------------------------------------------------------------------------

// OllamaClient is an HTTP client for Ollama's native API (/api/chat, /api/tags, /api/embeddings).
type OllamaClient struct {
	cfg        ProviderConfig
	http       *http.Client
	streamHTTP *http.Client
	logger     *zap.Logger
	breaker    *providerBreaker
}

// NewOllamaClient returns a new OllamaClient.
func NewOllamaClient(cfg ProviderConfig, logger *zap.Logger) *OllamaClient {
	streamTransport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	return &OllamaClient{
		cfg:        cfg,
		http:       &http.Client{Timeout: constants.LLMRequestTimeout},
		streamHTTP: &http.Client{Transport: streamTransport},
		logger:     logger,
		breaker:    &providerBreaker{state: cbClosed},
	}
}

// httpDo sends one HTTP POST request and returns body, status code, and headers.
func (c *OllamaClient) httpDo(ctx context.Context, path string, body []byte) ([]byte, int, http.Header, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%s: build request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, resp.Header, err
}

// httpDoStream is like httpDo but uses streamHTTP client.
func (c *OllamaClient) httpDoStream(ctx context.Context, path string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return c.streamHTTP.Do(httpReq)
}

// parseNDJSONStream scans the NDJSON response and accumulates into result.
func parseNDJSONStream(scanner *bufio.Scanner, onToken func(string)) CompletionResponse {
	var result CompletionResponse
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk ollamaChatResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if token := chunk.Message.Content; token != "" {
			result.Content += token
			onToken(token)
		}
		if chunk.Done {
			result.Usage.PromptTokens = chunk.PromptEvalCount
			result.Usage.CompletionTokens = chunk.EvalCount
			result.Usage.TotalTokens = chunk.PromptEvalCount + chunk.EvalCount
		}
	}
	return result
}

// Complete sends a non-streaming chat request to /api/chat.
func (c *OllamaClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	body, err := json.Marshal(c.buildChatRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.cfg.Name, err)
	}

	raw, err := c.retryUntilOK(ctx, "/api/chat", body)
	if err != nil {
		c.breaker.recordFailure()
		return nil, err
	}

	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", c.cfg.Name, err)
	}
	c.breaker.recordSuccess()
	return c.toCompletionResponse(&out), nil
}

// classifyStatus returns (error, shouldRetry) for a non-200 response.
func (c *OllamaClient) classifyStatus(status int, header http.Header) (error, bool) {
	if status == http.StatusTooManyRequests {
		sleepRetryAfter(context.Background(), header.Get("Retry-After"))
	}
	if !isRetryableHTTPStatus(status) {
		c.logger.Error(c.cfg.Name+": http error (no retry)", zap.Int("status", status))
		return fmt.Errorf("%s: POST %s 返回 %d，请检查 API Key 与 Base URL 是否正确（当前 kind=ollama）: %w",
			c.cfg.Name, strings.TrimSuffix(c.cfg.BaseURL, "/")+"/api/chat", status, domain.ErrUpstreamRequestFailed), false
	}
	return fmt.Errorf("%s: complete status %d", c.cfg.Name, status), true
}

// retryUntilOK sends body to path with retry+backoff and returns the raw OK body.
func (c *OllamaClient) retryUntilOK(ctx context.Context, path string, body []byte) ([]byte, error) {
	var lastErr error
	var retry bool
	for attempt := range maxRetryAttempts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%s: context cancelled: %w", c.cfg.Name, err)
		}
		if attempt > 0 {
			if err := sleepBackoff(ctx, attempt-1); err != nil {
				return nil, err
			}
		}

		raw, status, header, err := c.httpDo(ctx, path, body)
		if err != nil {
			lastErr = fmt.Errorf("%s: do request: %w", c.cfg.Name, err)
			continue
		}
		if status == http.StatusOK {
			return raw, nil
		}

		lastErr, retry = c.classifyStatus(status, header)
		if !retry {
			return nil, lastErr
		}
		c.logger.Warn(c.cfg.Name+": http error, retrying",
			zap.Int("status", status), zap.Int("attempt", attempt+1))
	}
	return nil, lastErr
}

// classifyStreamStatus returns (error, shouldRetry) for a non-200 stream response.
func (c *OllamaClient) classifyStreamStatus(status int, header http.Header) (error, bool) {
	if status == http.StatusTooManyRequests {
		sleepRetryAfter(context.Background(), header.Get("Retry-After"))
	}
	err := fmt.Errorf("%s: stream status %d", c.cfg.Name, status)
	if !isRetryableHTTPStatus(status) {
		c.logger.Error(c.cfg.Name+": stream error (no retry)", zap.Int("status", status))
		return err, false
	}
	return err, true
}

// drainNonOKStream handles a non-200 stream response. It drains the body and
// returns (lastErr, shouldRetry).
func (c *OllamaClient) drainNonOKStream(resp *http.Response, attempt int) (error, bool) {
	lastErr, retry := c.classifyStreamStatus(resp.StatusCode, resp.Header)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !retry {
		return lastErr, false
	}
	c.logger.Warn(c.cfg.Name+": stream error, retrying",
		zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
	return lastErr, true
}

// streamHTTPErr translates an HTTP error from httpDoStream. Non-nil return
// signals an idle timeout or other fatal error that must not be retried.
func (c *OllamaClient) streamHTTPErr(idleCtx context.Context, err error) error {
	if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return fmt.Errorf("%s: %w", c.cfg.Name, cause)
	}
	return nil
}

// connectAttempt makes one HTTP stream attempt. Returns (resp, nil, false) on
// success, (nil, err, false) on fatal error, (nil, err, true) on retryable error.
func (c *OllamaClient) connectAttempt(idleCtx context.Context, path string, body []byte, attempt int) (*http.Response, error, bool) {
	if err := idleCtx.Err(); err != nil {
		return nil, fmt.Errorf("%s: context cancelled: %w", c.cfg.Name, err), false
	}
	if attempt > 0 {
		if err := sleepBackoff(idleCtx, attempt-1); err != nil {
			return nil, err, false
		}
	}
	resp, err := c.httpDoStream(idleCtx, path, body)
	if err != nil {
		if cerr := c.streamHTTPErr(idleCtx, err); cerr != nil {
			return nil, cerr, false
		}
		return nil, fmt.Errorf("%s: do stream request: %w", c.cfg.Name, err), true
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil, false
	}
	lastErr, retry := c.drainNonOKStream(resp, attempt)
	if !retry {
		return nil, lastErr, false
	}
	return nil, lastErr, true
}

// connectStream establishes a streaming HTTP connection with retry+backoff.
func (c *OllamaClient) connectStream(ctx, idleCtx context.Context, path string, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := range maxRetryAttempts {
		resp, err, retry := c.connectAttempt(idleCtx, path, body, attempt)
		if resp != nil {
			return resp, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, lastErr
}

// CompleteStream sends a streaming chat request (NDJSON) to /api/chat.
func (c *OllamaClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	body, err := json.Marshal(c.buildChatRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal stream request: %w", c.cfg.Name, err)
	}

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

	resp, err := c.connectStream(ctx, idleCtx, "/api/chat", body)
	if err != nil {
		c.breaker.recordFailure()
		return nil, err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	result := parseNDJSONStream(scanner, wrappedOnToken)
	if err := scanner.Err(); err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			c.breaker.recordFailure()
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
		c.breaker.recordFailure()
		return nil, fmt.Errorf("%s: read stream: %w", c.cfg.Name, err)
	}
	c.breaker.recordSuccess()
	return &result, nil
}

// Health checks connectivity via GET /api/tags.
func (c *OllamaClient) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("%s: build health request: %w", c.cfg.Name, err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s: health check: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/api/tags"
		return fmt.Errorf("%s: GET %s 返回 %d，请检查 provider kind 与 Base URL 是否正确（当前 kind=ollama）: %w",
			c.cfg.Name, url, resp.StatusCode, domain.ErrUpstreamRequestFailed)
	}
	return nil
}

// ListModels discovers models via GET /api/tags.
func (c *OllamaClient) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build tags request: %w", c.cfg.Name, err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do tags request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read tags body: %w", c.cfg.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/api/tags"
		return nil, fmt.Errorf("%s: GET %s 返回 %d，请检查 provider kind 与 Base URL 是否正确（当前 kind=ollama）: %w",
			c.cfg.Name, url, resp.StatusCode, domain.ErrUpstreamRequestFailed)
	}

	var out ollamaTagsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode tags: %w", c.cfg.Name, err)
	}

	models := make([]string, len(out.Models))
	for i, m := range out.Models {
		models[i] = m.Name
	}
	return models, nil
}

// CreateEmbeddings sends a request to /api/embeddings.
func (c *OllamaClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: req.Model, Input: req.Input})
	if err != nil {
		return nil, fmt.Errorf("%s: marshal embed request: %w", c.cfg.Name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build embed request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do embed request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read embed body: %w", c.cfg.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Error(c.cfg.Name+": embed http error",
			zap.Int("status", resp.StatusCode),
		)
		return nil, fmt.Errorf("%s: embed status %d", c.cfg.Name, resp.StatusCode)
	}

	var out ollamaEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode embed response: %w", c.cfg.Name, err)
	}

	return &EmbeddingResponse{Embeddings: out.Embeddings}, nil
}

// BatchSize returns the configured batch size or default 100.
func (c *OllamaClient) BatchSize() int {
	if c.cfg.EmbedBatchSize > 0 {
		return c.cfg.EmbedBatchSize
	}
	return 100
}

func (c *OllamaClient) buildChatRequest(req *CompletionRequest, stream bool) ollamaChatRequest {
	messages := make([]ollamaChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = ollamaChatMessage{Role: m.Role, Content: m.Content}
	}

	ollamaReq := ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   stream,
		Options: ollamaChatOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
		},
	}

	if len(req.Tools) > 0 {
		ollamaReq.Tools = make([]ollamaToolDef, len(req.Tools))
		for i, t := range req.Tools {
			ollamaReq.Tools[i] = ollamaToolDef{
				Type: t.Type,
				Function: ollamaToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			}
		}
	}

	return ollamaReq
}

func (c *OllamaClient) toCompletionResponse(resp *ollamaChatResponse) *CompletionResponse {
	return &CompletionResponse{
		Content: resp.Message.Content,
		Model:   resp.Model,
		Usage: TokenUsage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		},
	}
}

// ---------------------------------------------------------------------------
// sleepBackoff sleeps for an exponential backoff delay, respecting context cancellation.
func sleepBackoff(ctx context.Context, attempt int) error {
	delay := calculateBackoffWithJitter(attempt, retryBaseDelay)
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
	}
}

// sleepRetryAfter sleeps for the Retry-After duration if present.
func sleepRetryAfter(ctx context.Context, header string) {
	if delay := parseRetryAfter(header); delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}
}

// ollamaProtocol — stateless ChatProtocol + EmbedProtocol adapter
// ---------------------------------------------------------------------------

// OllamaProtocol satisfies ChatProtocol and EmbedProtocol.
type OllamaProtocol struct {
	client   *OllamaClient
	breakers sync.Map
}

// NewOllamaProtocol returns a protocol adapter backed by client.
func NewOllamaProtocol(client *OllamaClient) *OllamaProtocol {
	return &OllamaProtocol{client: client}
}

func (p *OllamaProtocol) clientFor(cfg ProviderConfig) *OllamaClient {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.Name+"\x00"+cfg.BaseURL+"\x00"+cfg.APIKey)))
	breaker, _ := p.breakers.LoadOrStore(key, &providerBreaker{state: cbClosed})
	return &OllamaClient{
		cfg:        cfg,
		http:       p.client.http,
		streamHTTP: p.client.streamHTTP,
		logger:     p.client.logger,
		breaker:    breaker.(*providerBreaker),
	}
}

// Complete implements ChatProtocol.
func (p *OllamaProtocol) Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
	return p.clientFor(cfg).Complete(ctx, req)
}

// CompleteStream implements ChatProtocol.
func (p *OllamaProtocol) CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	return p.clientFor(cfg).CompleteStream(ctx, req, onToken)
}

// Health implements ChatProtocol.
func (p *OllamaProtocol) Health(ctx context.Context, cfg ProviderConfig) error {
	return p.clientFor(cfg).Health(ctx)
}

// ListModels implements ChatProtocol.
func (p *OllamaProtocol) ListModels(ctx context.Context, cfg ProviderConfig) ([]string, error) {
	return p.clientFor(cfg).ListModels(ctx)
}

// CreateEmbeddings implements EmbedProtocol.
func (p *OllamaProtocol) CreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	return p.clientFor(cfg).CreateEmbeddings(ctx, req)
}

// BatchSize implements EmbedProtocol.
func (p *OllamaProtocol) BatchSize() int {
	return p.client.BatchSize()
}

var (
	_ ChatProtocol  = (*OllamaProtocol)(nil)
	_ EmbedProtocol = (*OllamaProtocol)(nil)
)

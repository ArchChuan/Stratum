package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

const (
	maxRetryAttempts   = 3
	retryBaseDelay     = 100 * time.Millisecond
	maxRetryDelay      = 10 * time.Second
	cbFailureThreshold = 5
	cbRecoveryTimeout  = 30 * time.Second
)

// providerBreaker is a per-provider three-state circuit breaker.
type providerBreaker struct {
	state         int // 0=closed, 1=open, 2=half-open
	failures      int
	lastFailureAt time.Time
	probing       bool
	mu            sync.Mutex
}

const (
	cbClosed   = 0
	cbOpen     = 1
	cbHalfOpen = 2
)

func (b *providerBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case cbOpen:
		if time.Since(b.lastFailureAt) >= cbRecoveryTimeout {
			b.state = cbHalfOpen
			b.probing = false
		} else {
			return false
		}
		fallthrough
	case cbHalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	default: // cbClosed
		return true
	}
}

func (b *providerBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = cbClosed
	b.failures = 0
	b.probing = false
}

func (b *providerBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastFailureAt = time.Now()
	b.probing = false
	if b.failures >= cbFailureThreshold {
		b.state = cbOpen
	}
}

// isRetryableHTTPStatus returns true for HTTP status codes that warrant retry
func isRetryableHTTPStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	}
	return statusCode >= 500 // all 5xx
}

// calculateBackoffWithJitter returns exponential backoff delay with ±50% jitter
func calculateBackoffWithJitter(attempt int, baseDelay time.Duration) time.Duration {
	delay := min(time.Duration(float64(baseDelay)*math.Pow(2, float64(attempt))), maxRetryDelay)
	// Add jitter: ±50%
	jitter := time.Duration(rand.Int63n(int64(delay))) // #nosec G404
	return delay/2 + jitter
}

// parseRetryAfter extracts delay from Retry-After header (seconds or HTTP-date)
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	// Try parsing as seconds first
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		delay := time.Duration(seconds) * time.Second
		if delay > maxRetryDelay {
			return maxRetryDelay
		}
		return delay
	}
	// Try parsing as HTTP-date (RFC1123)
	if t, err := http.ParseTime(header); err == nil {
		delay := time.Until(t)
		if delay > 0 && delay <= maxRetryDelay {
			return delay
		}
	}
	return 0
}

// ProviderConfig holds the minimal configuration that differentiates one
// OpenAI-compatible provider from another.
type ProviderConfig struct {
	Name           string
	BaseURL        string
	APIKey         string
	HealthModel    string
	Models         []string
	EmbedBatchSize int // max texts per embedding request; 0 = use default (100)
}

// OpenAICompatClient implements LLMClient, StreamingLLMClient, and
// EmbeddingClient for any provider that speaks the OpenAI-compatible protocol.
type OpenAICompatClient struct {
	cfg        ProviderConfig
	http       *http.Client // non-streaming: flat Timeout guards stuck complete calls
	streamHTTP *http.Client // streaming: transport-level timeouts only, no flat Timeout
	logger     *zap.Logger
	breaker    *providerBreaker
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
		breaker:    &providerBreaker{state: cbClosed},
	}
}

func (c *OpenAICompatClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.cfg.Name, err)
	}

	var lastErr error
	for attempt := range maxRetryAttempts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%s: context cancelled: %w", c.cfg.Name, err)
		}
		if attempt > 0 {
			delay := calculateBackoffWithJitter(attempt-1, retryBaseDelay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, fmt.Errorf("%s: context cancelled during retry: %w", c.cfg.Name, ctx.Err())
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %w", c.cfg.Name, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

		resp, err := c.http.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("%s: do request: %w", c.cfg.Name, err)
			continue
		}

		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("%s: read body: %w", c.cfg.Name, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			// 429: respect Retry-After before next attempt
			if resp.StatusCode == http.StatusTooManyRequests {
				if delay := parseRetryAfter(resp.Header.Get("Retry-After")); delay > 0 {
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return nil, fmt.Errorf("%s: context cancelled waiting Retry-After: %w", c.cfg.Name, ctx.Err())
					}
				}
			}
			lastErr = fmt.Errorf("%s: status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
			if !isRetryableHTTPStatus(resp.StatusCode) {
				c.logger.Error(c.cfg.Name+": http error (no retry)",
					zap.String("model", req.Model),
					zap.Int("status", resp.StatusCode),
				)
				return nil, lastErr
			}
			c.logger.Warn(c.cfg.Name+": http error, retrying",
				zap.String("model", req.Model),
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt+1),
			)
			continue
		}

		var out openAICompletionResp
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("%s: decode response: %w", c.cfg.Name, err)
		}
		if len(out.Choices) == 0 {
			return nil, fmt.Errorf("%s: no choices in response", c.cfg.Name)
		}
		c.breaker.recordSuccess()
		return &CompletionResponse{
			Content:   out.Choices[0].Message.Content,
			Model:     out.Model,
			ToolCalls: out.Choices[0].Message.ToolCalls,
			Usage:     out.Usage,
		}, nil
	}

	c.breaker.recordFailure()
	c.logger.Error(c.cfg.Name+": all retries exhausted",
		zap.String("model", req.Model),
		zap.Int("attempts", maxRetryAttempts),
		zap.Error(lastErr),
	)
	return nil, lastErr
}

func (c *OpenAICompatClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	streamReq := *req
	streamReq.Stream = true
	body, err := json.Marshal(streamReq)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal stream request: %w", c.cfg.Name, err)
	}

	// Idle-token watchdog: cancel the request if no token arrives for LLMStreamIdleTimeout.
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

	// Retry until stream connection established (status 200)
	var resp *http.Response
	var lastErr error
	for attempt := range maxRetryAttempts {
		if err := idleCtx.Err(); err != nil {
			return nil, fmt.Errorf("%s: context cancelled: %w", c.cfg.Name, err)
		}
		if attempt > 0 {
			delay := calculateBackoffWithJitter(attempt-1, retryBaseDelay)
			select {
			case <-time.After(delay):
			case <-idleCtx.Done():
				return nil, fmt.Errorf("%s: context cancelled during retry: %w", c.cfg.Name, idleCtx.Err())
			}
		}

		httpReq, err := http.NewRequestWithContext(idleCtx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: build stream request: %w", c.cfg.Name, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err = c.streamHTTP.Do(httpReq)
		if err != nil {
			if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
				return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
			}
			lastErr = fmt.Errorf("%s: do stream request: %w", c.cfg.Name, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close() // #nosec G104
			// 429: respect Retry-After
			if resp.StatusCode == http.StatusTooManyRequests {
				if delay := parseRetryAfter(resp.Header.Get("Retry-After")); delay > 0 {
					select {
					case <-time.After(delay):
					case <-idleCtx.Done():
						return nil, fmt.Errorf("%s: context cancelled waiting Retry-After: %w", c.cfg.Name, idleCtx.Err())
					}
				}
			}
			lastErr = fmt.Errorf("%s: stream status %d: %s", c.cfg.Name, resp.StatusCode, string(raw))
			if !isRetryableHTTPStatus(resp.StatusCode) {
				c.logger.Error(c.cfg.Name+": stream error (no retry)",
					zap.String("model", req.Model),
					zap.Int("status", resp.StatusCode),
				)
				return nil, lastErr
			}
			c.logger.Warn(c.cfg.Name+": stream error, retrying",
				zap.String("model", req.Model),
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt+1),
			)
			continue
		}
		// Success: stream established (200 OK), break to read stream
		break
	}

	if resp == nil || resp.StatusCode != http.StatusOK {
		c.breaker.recordFailure()
		c.logger.Error(c.cfg.Name+": stream retries exhausted",
			zap.String("model", req.Model),
			zap.Int("attempts", maxRetryAttempts),
			zap.Error(lastErr),
		)
		return nil, lastErr
	}
	defer resp.Body.Close() //nolint:errcheck

	// Stream established, read SSE events (no retry from here)
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
			c.breaker.recordFailure()
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
		c.breaker.recordFailure()
		return nil, fmt.Errorf("%s: read stream: %w", c.cfg.Name, err)
	}
	c.breaker.recordSuccess()
	return &result, nil
}

func (c *OpenAICompatClient) BatchSize() int {
	if c.cfg.EmbedBatchSize > 0 {
		return c.cfg.EmbedBatchSize
	}
	return 100
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

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
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/safetext"
	"go.uber.org/zap"
)

const (
	maxRetryAttempts          = 3
	retryBaseDelay            = 100 * time.Millisecond
	maxRetryDelay             = 10 * time.Second
	cbFailureThreshold        = 5
	cbRecoveryTimeout         = 30 * time.Second
	maxModelListResponseBytes = 1 << 20
	// streamErrorBodyMaxBytes 是错误响应体读取上限：错误 body 只用于
	// context_length 语义识别与日志/错误详情，截断即可，防止恶意或异常
	// 上游返回超大 body 撑爆内存。
	streamErrorBodyMaxBytes = 4096
	// maxModelDiscoveryPages 是模型发现翻页的安全上限。OpenAI /models 默认
	// 每页 20 个、翻页每页 100 个，20 页可覆盖 2000+ 模型；同时防止
	// has_more 恒为 true 的异常上游导致死循环。
	maxModelDiscoveryPages = 20
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

// isContextLengthBody 探测 OpenAI-compat 400 响应体的 error.code /
// error.message 是否标记上下文超限。字节级前置过滤避免无谓 json.Unmarshal；
// message 可能写作 "context length"（空格）而非 "context_length"（下划线），
// 两种形式都必须放行到 JSON 解析。
func isContextLengthBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if !bytes.Contains(body, []byte("context_length")) && !bytes.Contains(body, []byte("context length")) {
		return false
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	code := strings.ToLower(payload.Error.Code)
	return code == "context_length_exceeded" ||
		strings.Contains(strings.ToLower(payload.Error.Message), "context_length_exceeded") ||
		strings.Contains(strings.ToLower(payload.Error.Message), "maximum context length")
}

// maxTokensFallback 网关防御层：MaxTokens<=0 时兜底 DefaultOutputReserveTokens
// （供应商要求 minimum:1）。按值传参会复制 req，返回副本，
// 禁止就地修改调用方可见的 req 对象。
func maxTokensFallback(req CompletionRequest) CompletionRequest {
	if req.MaxTokens <= 0 {
		req.MaxTokens = constants.DefaultOutputReserveTokens
	}
	return req
}

// openaiModelsResponse is the JSON body from GET /models (OpenAI-compatible).
// has_more/last_id 来自 OpenAI 官方分页契约；不返回分页字段的兼容网关
// 两者为零值，翻页自然止于第一页，行为与分页改造前一致。
type openaiModelsResponse struct {
	Data    []openaiModelItem `json:"data"`
	HasMore bool              `json:"has_more"`
	LastID  string            `json:"last_id"`
}

type openaiModelItem struct {
	ID string `json:"id"`
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

// OpenAICompatClient is an OpenAI-compatible provider that implements
// ChatProtocol and EmbedProtocol.
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

	// 网关防御层：上层调用方（memory 等）可能传 MaxTokens<=0，供应商要求
	// minimum:1。maxTokensFallback 复制后兜底，禁止就地修改调用方可见的 req。
	marshalReq := maxTokensFallback(*req)
	body, err := json.Marshal(&marshalReq)
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
			shouldRetry, statusErr := c.handleCompleteStatus(ctx, resp, raw, req.Model, attempt)
			if !shouldRetry {
				return nil, statusErr
			}
			lastErr = statusErr
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

// redactedBodySummary 返回错误响应体的脱敏摘要：先截断到
// streamErrorBodyMaxBytes 字节，再经 safetext.RedactCredentials 脱敏，
// 保证 authorization/password/token/api key/secret 键值不落入日志或错误
// 正文。空 body 返回空串。
func redactedBodySummary(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > streamErrorBodyMaxBytes {
		raw = raw[:streamErrorBodyMaxBytes]
	}
	return safetext.RedactCredentials(string(raw))
}

// errorMessageFromBody 从 OpenAI 兼容错误响应体中提取语义化 error.message。
// 仅当 body 是 JSON 且 error 字段是对象（`{"error":{"message":"..."}}`，
// 智谱/OpenAI 标准格式）时返回截断+脱敏后的 message；非 JSON 或 error 非
// 对象（body 可能是裸文本/HTML/二进制等任意上游内容）返回空串——原始
// 响应体不得落入下游错误正文（红线）。日志侧仍记 redactedBodySummary 的
// 完整脱敏摘要。
func errorMessageFromBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Error.Message == "" {
		return ""
	}
	return redactedBodySummary([]byte(payload.Error.Message))
}

// streamErrorFromResponse 处理流式非 200 响应：读取限长响应体，识别
// 400 context_length_exceeded 为永久语义错误（重试不可恢复，供 agent 层
// 感知降级）；否则构造带结构化 error.message 详情的错误。返回错误本身与
// 供日志的脱敏 body 摘要（RedactCredentials 已过滤凭据；非 JSON 或裸文本
// 上游 body 不进错误正文，仅进脱敏日志）。
func (c *OpenAICompatClient) streamErrorFromResponse(resp *http.Response) (bodySummary string, lastErr error) {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, streamErrorBodyMaxBytes))
	if resp.StatusCode == http.StatusBadRequest && isContextLengthBody(raw) {
		return redactedBodySummary(raw), fmt.Errorf("%s: %w", c.cfg.Name, ErrContextLengthExceeded)
	}
	bodySummary = redactedBodySummary(raw)
	if detail := errorMessageFromBody(raw); detail != "" {
		return bodySummary, fmt.Errorf("%s: stream status %d: %s", c.cfg.Name, resp.StatusCode, detail)
	}
	return bodySummary, fmt.Errorf("%s: stream status %d", c.cfg.Name, resp.StatusCode)
}

// handleCompleteStatus 处理 Complete 的非 200 响应：识别 400
// context_length_exceeded 为永久语义错误（重试不可恢复，供 agent 层感知
// 降级）；429 按 Retry-After 等待；其余按 isRetryableHTTPStatus 分类。
// 返回 (shouldRetry, err)：shouldRetry=true 时 err 为本次尝试的错误。
func (c *OpenAICompatClient) handleCompleteStatus(
	ctx context.Context,
	resp *http.Response,
	raw []byte,
	model string,
	attempt int,
) (bool, error) {
	// 429: respect Retry-After before next attempt
	if resp.StatusCode == http.StatusTooManyRequests {
		if delay := parseRetryAfter(resp.Header.Get("Retry-After")); delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return false, fmt.Errorf("%s: context cancelled waiting Retry-After: %w", c.cfg.Name, ctx.Err())
			}
		}
	}
	// 400 context_length_exceeded 语义化：重试不可恢复，标记永久错误
	// 供 agent 层感知降级。
	if resp.StatusCode == http.StatusBadRequest && isContextLengthBody(raw) {
		return false, fmt.Errorf("%s: %w", c.cfg.Name, ErrContextLengthExceeded)
	}
	var lastErr error
	if detail := errorMessageFromBody(raw); detail != "" {
		lastErr = fmt.Errorf("%s: complete status %d: %s", c.cfg.Name, resp.StatusCode, detail)
	} else {
		lastErr = fmt.Errorf("%s: complete status %d", c.cfg.Name, resp.StatusCode)
	}
	if !isRetryableHTTPStatus(resp.StatusCode) {
		c.logger.Error(c.cfg.Name+": http error (no retry)",
			zap.String("model", model),
			zap.Int("status", resp.StatusCode),
		)
		return false, lastErr
	}
	c.logger.Warn(c.cfg.Name+": http error, retrying",
		zap.String("model", model),
		zap.Int("status", resp.StatusCode),
		zap.Int("attempt", attempt+1),
	)
	return true, lastErr
}

func (c *OpenAICompatClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	streamReq := *req
	streamReq.Stream = true
	body, err := json.Marshal(maxTokensFallback(streamReq))
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
			// 读错误响应体（限长+脱敏）用于 context_length 语义识别与日志；
			// body 可能较大，只保留前 streamErrorBodyMaxBytes 字节。
			var errBody string
			errBody, lastErr = c.streamErrorFromResponse(resp)
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
			if !isRetryableHTTPStatus(resp.StatusCode) {
				c.logger.Error(c.cfg.Name+": stream error (no retry)",
					zap.String("model", req.Model),
					zap.Int("status", resp.StatusCode),
					zap.String("error_body", errBody),
				)
				return nil, lastErr
			}
			c.logger.Warn(c.cfg.Name+": stream error, retrying",
				zap.String("model", req.Model),
				zap.Int("status", resp.StatusCode),
				zap.String("error_body", errBody),
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
	result, err := c.readStreamBody(resp, idleCtx, req.Model, wrappedOnToken)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// readStreamBody drains a successfully-established SSE response into a
// CompletionResponse, classifying premature EOF / truncation as errors.
func (c *OpenAICompatClient) readStreamBody(resp *http.Response, idleCtx context.Context, model string, onToken func(string)) (*CompletionResponse, error) {
	defer resp.Body.Close() //nolint:errcheck

	var result CompletionResponse
	tcAcc := make(map[int]*streamToolCallDelta)
	scanner := bufio.NewScanner(resp.Body)
	streamDone, chunksWithContent := c.scanStream(scanner, &result, tcAcc, onToken)
	// convert accumulated deltas to ToolCall slice ordered by index
	result.ToolCalls = convertToolCalls(tcAcc)
	if err := scanner.Err(); err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			c.breaker.recordFailure()
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
		c.breaker.recordFailure()
		return nil, fmt.Errorf("%s: read stream: %w", c.cfg.Name, err)
	}
	if !streamDone {
		c.breaker.recordFailure()
		if result.Content == "" && len(tcAcc) == 0 {
			// 首个 chunk 前断开：连接正常建立但未产出任何内容，视为普通错误。
			c.logger.Warn(c.cfg.Name+": stream ended before any data",
				zap.String("model", model))
			return nil, fmt.Errorf("%s: stream ended before any data: %w", c.cfg.Name, io.EOF)
		}
		// 内容已开始输出但未收尾：截断，绝不 recordSuccess。
		c.logger.Warn(c.cfg.Name+": stream truncated",
			zap.String("model", model),
			zap.Int("chunks", chunksWithContent))
		return nil, fmt.Errorf("%s: stream truncated: %w", c.cfg.Name, domain.ErrStreamTruncated)
	}
	c.breaker.recordSuccess()
	return &result, nil
}

// scanStream reads SSE chunks until EOF or a termination marker ([DONE] or a
// chunk with finish_reason). Returns whether a termination marker was seen and
// the number of chunks carrying a non-empty content delta (截断日志用；每个
// chunk 至多计 1，不是 token 数)。
func (c *OpenAICompatClient) scanStream(
	scanner *bufio.Scanner,
	result *CompletionResponse,
	tcAcc map[int]*streamToolCallDelta,
	onToken func(string),
) (bool, int) {
	streamDone := false
	chunksWithContent := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			return true, chunksWithContent
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		finishSeen, contentChunks := applyStreamChunk(result, tcAcc, chunk, onToken)
		chunksWithContent += contentChunks
		if finishSeen {
			streamDone = true
		}
	}
	return streamDone, chunksWithContent
}

// applyStreamChunk merges one SSE chunk into result and returns whether a
// finish_reason termination marker was seen, plus the number of chunks with
// content emitted by this chunk (0 或 1)。兼容仅发 finish_reason、不发
// [DONE] 的 provider（如部分代理）。
func applyStreamChunk(
	result *CompletionResponse,
	tcAcc map[int]*streamToolCallDelta,
	chunk openAIStreamChunk,
	onToken func(string),
) (bool, int) {
	finishSeen := false
	contentChunks := 0
	if chunk.Model != "" {
		result.Model = chunk.Model
	}
	if chunk.Usage != nil {
		result.Usage.PromptTokens = chunk.Usage.PromptTokens
		result.Usage.CompletionTokens = chunk.Usage.CompletionTokens
		result.Usage.TotalTokens = chunk.Usage.TotalTokens
	}
	if len(chunk.Choices) > 0 {
		if chunk.Choices[0].FinishReason != "" {
			finishSeen = true
		}
		if t := chunk.Choices[0].Delta.Content; t != "" {
			result.Content += t
			contentChunks++
			onToken(t)
		}
		accumulateToolDeltas(tcAcc, chunk.Choices[0].Delta.ToolCalls)
	}
	return finishSeen, contentChunks
}

// accumulateToolDeltas merges per-chunk tool-call fragments into tcAcc by index.
func accumulateToolDeltas(tcAcc map[int]*streamToolCallDelta, deltas []streamToolCallDelta) {
	for _, d := range deltas {
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

// convertToolCalls converts accumulated deltas to a ToolCall slice ordered by index.
func convertToolCalls(tcAcc map[int]*streamToolCallDelta) []ToolCall {
	result := make([]ToolCall, len(tcAcc))
	for idx, acc := range tcAcc {
		if idx < len(result) {
			result[idx] = ToolCall{
				ID:   acc.ID,
				Type: acc.Type,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: acc.Function.Name, Arguments: acc.Function.Arguments},
			}
		}
	}
	return result
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
		return nil, fmt.Errorf("%s: embed status %d", c.cfg.Name, resp.StatusCode)
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

// ListModels discovers models via GET /models with static context-window fallback.
func (c *OpenAICompatClient) ListModels(ctx context.Context) ([]DiscoveredModel, error) {
	names, err := c.fetchModelNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveredModel, len(names))
	for i, name := range names {
		ctxWin, maxOut := lookupModelSpec(name)
		out[i] = DiscoveredModel{Name: name, ContextWindow: ctxWin, MaxOutputTokens: maxOut}
	}
	return out, nil
}

// fetchModelNames calls GET /models and returns the raw name list, following
// OpenAI's has_more/last_id pagination. The first page omits query params for
// maximum compatibility with strict OpenAI-compat gateways; only a page that
// explicitly reports has_more=true and a non-empty last_id triggers follow-ups.
func (c *OpenAICompatClient) fetchModelNames(ctx context.Context) ([]string, error) {
	base := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/models"
	var names []string
	after := ""
	for page := 0; page < maxModelDiscoveryPages; page++ {
		u := base
		if after != "" {
			u = base + "?limit=100&after=" + url.QueryEscape(after)
		}
		out, err := c.fetchModelPage(ctx, u)
		if err != nil {
			return nil, err
		}
		for _, m := range out.Data {
			names = append(names, m.ID)
		}
		if !out.HasMore || out.LastID == "" {
			break
		}
		after = out.LastID
	}
	return names, nil
}

// fetchModelPage fetches and decodes a single page from GET /models.
func (c *OpenAICompatClient) fetchModelPage(ctx context.Context, u string) (*openaiModelsResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build models request: %w", c.cfg.Name, err)
	}
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do models request: %w", c.cfg.Name, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxModelListResponseBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%s: read models body: %w", c.cfg.Name, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%s: close models response: %w", c.cfg.Name, closeErr)
	}
	if len(raw) > maxModelListResponseBytes {
		return nil, fmt.Errorf("%s: GET %s response exceeds size limit", c.cfg.Name, u)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: GET %s returned %d: check API key and base URL",
			c.cfg.Name, u, resp.StatusCode)
	}
	var out openaiModelsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode models: %w", c.cfg.Name, err)
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// ChatProtocol adapters — stateless wrappers that accept a ProviderConfig at
// call time, delegating to the existing instance-method implementations.
// These verify the new interface contracts compile; full stateless migration
// is deferred.
// ---------------------------------------------------------------------------

// ChatComplete delegates to the instance's Complete method.
func (c *OpenAICompatClient) ChatComplete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
	return c.Complete(ctx, req)
}

// ChatCompleteStream delegates to the instance's CompleteStream method.
func (c *OpenAICompatClient) ChatCompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	return c.CompleteStream(ctx, req, onToken)
}

// ChatHealth delegates to the instance's Health method.
func (c *OpenAICompatClient) ChatHealth(ctx context.Context, cfg ProviderConfig) error {
	return c.Health(ctx)
}

// ChatListModels delegates to the instance's ListModels method.
func (c *OpenAICompatClient) ChatListModels(ctx context.Context, cfg ProviderConfig) ([]DiscoveredModel, error) {
	return c.ListModels(ctx)
}

// EmbedCreateEmbeddings delegates to the instance's CreateEmbeddings method.
func (c *OpenAICompatClient) EmbedCreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	return c.CreateEmbeddings(ctx, req)
}

// EmbedBatchSize is an alias for BatchSize that satisfies the EmbedProtocol contract.
// It shadows the existing BatchSize method but is identical in behaviour.
func (c *OpenAICompatClient) EmbedBatchSize() int {
	return c.BatchSize()
}

// ---------------------------------------------------------------------------
// OpenAICompatProtocol adapts tenant-resolved provider configuration to the
// shared OpenAI-compatible HTTP clients. Circuit-breaker state is isolated per
// resolved provider configuration while transports remain shared.
// ---------------------------------------------------------------------------

// OpenAICompatProtocol satisfies ChatProtocol and EmbedProtocol.
type OpenAICompatProtocol struct {
	client   *OpenAICompatClient
	breakers sync.Map
}

// NewOpenAICompatProtocol returns a protocol adapter backed by client.
func NewOpenAICompatProtocol(client *OpenAICompatClient) *OpenAICompatProtocol {
	return &OpenAICompatProtocol{client: client}
}

func (p *OpenAICompatProtocol) clientFor(cfg ProviderConfig) *OpenAICompatClient {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.Name+"\x00"+cfg.BaseURL+"\x00"+cfg.APIKey)))
	breaker, _ := p.breakers.LoadOrStore(key, &providerBreaker{state: cbClosed})
	return &OpenAICompatClient{
		cfg:        cfg,
		http:       p.client.http,
		streamHTTP: p.client.streamHTTP,
		logger:     p.client.logger,
		breaker:    breaker.(*providerBreaker),
	}
}

func (p *OpenAICompatProtocol) Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
	return p.clientFor(cfg).Complete(ctx, req)
}

func (p *OpenAICompatProtocol) CompleteStream(
	ctx context.Context,
	cfg ProviderConfig,
	req *CompletionRequest,
	onToken func(string),
) (*CompletionResponse, error) {
	return p.clientFor(cfg).CompleteStream(ctx, req, onToken)
}

func (p *OpenAICompatProtocol) Health(ctx context.Context, cfg ProviderConfig) error {
	return p.clientFor(cfg).Health(ctx)
}

func (p *OpenAICompatProtocol) ListModels(ctx context.Context, cfg ProviderConfig) ([]DiscoveredModel, error) {
	return p.clientFor(cfg).ListModels(ctx)
}

func (p *OpenAICompatProtocol) CreateEmbeddings(
	ctx context.Context,
	cfg ProviderConfig,
	req *EmbeddingRequest,
) (*EmbeddingResponse, error) {
	return p.clientFor(cfg).CreateEmbeddings(ctx, req)
}

func (p *OpenAICompatProtocol) BatchSize() int {
	return p.client.BatchSize()
}

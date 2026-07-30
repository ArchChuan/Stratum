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

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

const anthropicVersion = "2023-06-01"

// ---------------------------------------------------------------------------
// Anthropic Messages API types
// ---------------------------------------------------------------------------

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	Tools     []anthropicToolDef `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
}

type anthropicToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicMessagesResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Model   string                  `json:"model"`
	Usage   anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ---------------------------------------------------------------------------
// Anthropic SSE event types
// ---------------------------------------------------------------------------

type anthropicSSEEvent struct {
	Type    string             `json:"type"`
	Index   int                `json:"index"`
	Delta   anthropicSSEDelta  `json:"delta"`
	Usage   *anthropicSSEUsage `json:"usage"`
	Message *struct {
		ID    string             `json:"id"`
		Model string             `json:"model"`
		Usage *anthropicSSEUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	} `json:"content_block"`
}

type anthropicSSEDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

type anthropicSSEUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicModelsResponse is the JSON body from GET /v1/models.
type anthropicModelsResponse struct {
	Data []anthropicModelItem `json:"data"`
}

type anthropicModelItem struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	ContextWindow int    `json:"context_window"`
}

// ---------------------------------------------------------------------------
// AnthropicClient
// ---------------------------------------------------------------------------

// AnthropicClient is an HTTP client for Anthropic's Messages API (/v1/messages).
type AnthropicClient struct {
	cfg        ProviderConfig
	http       *http.Client
	streamHTTP *http.Client
	logger     *zap.Logger
	breaker    *providerBreaker
}

// NewAnthropicClient returns a new AnthropicClient.
func NewAnthropicClient(cfg ProviderConfig, logger *zap.Logger) *AnthropicClient {
	streamTransport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	return &AnthropicClient{
		cfg:        cfg,
		http:       &http.Client{Timeout: constants.LLMRequestTimeout},
		streamHTTP: &http.Client{Transport: streamTransport},
		logger:     logger,
		breaker:    &providerBreaker{state: cbClosed},
	}
}

// httpDo sends one HTTP POST request with Anthropic auth headers.
func (c *AnthropicClient) httpDo(ctx context.Context, path string, body []byte) ([]byte, int, http.Header, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%s: build request: %w", c.cfg.Name, err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, resp.Header, err
}

// httpDoStream is like httpDo but uses streamHTTP client and returns the open response.
func (c *AnthropicClient) httpDoStream(ctx context.Context, path string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", c.cfg.Name, err)
	}
	c.setHeaders(httpReq)
	return c.streamHTTP.Do(httpReq)
}

// classifyStatus returns (error, shouldRetry) for a non-200 chat response.
func (c *AnthropicClient) classifyStatus(status int, header http.Header) (error, bool) {
	if status == http.StatusTooManyRequests {
		sleepRetryAfter(context.Background(), header.Get("Retry-After"))
	}
	err := fmt.Errorf("%s: complete status %d", c.cfg.Name, status)
	if !isRetryableHTTPStatus(status) {
		c.logger.Error(c.cfg.Name+": http error (no retry)", zap.Int("status", status))
		return err, false
	}
	return err, true
}

// classifyStreamStatus returns (error, shouldRetry) for a non-200 stream response.
func (c *AnthropicClient) classifyStreamStatus(status int, header http.Header) (error, bool) {
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

// sseParser accumulates Anthropic SSE events into a CompletionResponse.
type sseParser struct {
	result     CompletionResponse
	toolUseAcc map[int]*toolUseBuilder
}

func newSSEParser() *sseParser {
	return &sseParser{toolUseAcc: make(map[int]*toolUseBuilder)}
}

// handleEvent processes one Anthropic SSE event.
func (p *sseParser) handleEvent(currentEvent string, evt anthropicSSEEvent, onToken func(string)) {
	switch currentEvent {
	case "message_start":
		p.handleMessageStart(evt)
	case "content_block_start":
		p.handleBlockStart(evt)
	case "content_block_delta":
		p.handleBlockDelta(evt, onToken)
	case "message_delta":
		p.handleMessageDelta(evt)
	}
}

func (p *sseParser) handleMessageStart(evt anthropicSSEEvent) {
	if evt.Message != nil {
		p.result.Model = evt.Message.Model
		if evt.Message.Usage != nil {
			p.result.Usage.PromptTokens = evt.Message.Usage.InputTokens
		}
	}
}

func (p *sseParser) handleBlockStart(evt anthropicSSEEvent) {
	if evt.ContentBlock == nil {
		return
	}
	if evt.ContentBlock.Type == "tool_use" {
		p.toolUseAcc[evt.Index] = &toolUseBuilder{name: evt.ContentBlock.Name}
	} else if evt.ContentBlock.Type == "text" {
		p.toolUseAcc[evt.Index] = nil
	}
}

func (p *sseParser) handleBlockDelta(evt anthropicSSEEvent, onToken func(string)) {
	switch evt.Delta.Type {
	case "text_delta":
		p.result.Content += evt.Delta.Text
		onToken(evt.Delta.Text)
	case "input_json_delta":
		if b, ok := p.toolUseAcc[evt.Index]; ok && b != nil {
			b.args.WriteString(evt.Delta.PartialJSON)
		}
	}
}

func (p *sseParser) handleMessageDelta(evt anthropicSSEEvent) {
	if evt.Usage != nil {
		p.result.Usage.CompletionTokens = evt.Usage.OutputTokens
		p.result.Usage.TotalTokens = p.result.Usage.PromptTokens + p.result.Usage.CompletionTokens
	}
}

// finalize converts accumulated tool_use blocks to ToolCalls and returns the result.
func (p *sseParser) finalize() CompletionResponse {
	for _, b := range p.toolUseAcc {
		if b == nil || b.args.Len() == 0 {
			continue
		}
		p.result.ToolCalls = append(p.result.ToolCalls, ToolCall{
			ID:   b.id,
			Type: "tool_use",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: b.name, Arguments: b.args.String()},
		})
	}
	return p.result
}

// Complete sends a non-streaming request to /v1/messages.
func (c *AnthropicClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	body, err := json.Marshal(c.buildRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.cfg.Name, err)
	}

	raw, err := c.retryUntilOK(ctx, "/v1/messages", body)
	if err != nil {
		c.breaker.recordFailure()
		return nil, err
	}

	var out anthropicMessagesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", c.cfg.Name, err)
	}
	c.breaker.recordSuccess()
	return c.toCompletionResponse(&out), nil
}

// retryUntilOK sends body to path with retry+backoff and returns the raw OK body.
func (c *AnthropicClient) retryUntilOK(ctx context.Context, path string, body []byte) ([]byte, error) {
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
			lastErr = fmt.Errorf("%s: status %d body=%s", c.cfg.Name, status, string(raw))
			c.logger.Error(c.cfg.Name+": http error (no retry)", zap.Int("status", status))
			return nil, lastErr
		}
		c.logger.Warn(c.cfg.Name+": http error, retrying",
			zap.Int("status", status), zap.Int("attempt", attempt+1))
	}
	return nil, lastErr
}

// scanSSE reads the SSE stream and populates the parser.
func scanSSE(scanner *bufio.Scanner, sp *sseParser, onToken func(string), idleTimer *time.Timer) error {
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			idleTimer.Reset(constants.LLMStreamIdleTimeout)
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		var evt anthropicSSEEvent
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}
		sp.handleEvent(currentEvent, evt, onToken)
	}
	return scanner.Err()
}

// CompleteStream sends a streaming request to /v1/messages (SSE).
func (c *AnthropicClient) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	if !c.breaker.allow() {
		return nil, fmt.Errorf("%s: circuit breaker open", c.cfg.Name)
	}

	body, err := json.Marshal(c.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal stream request: %w", c.cfg.Name, err)
	}

	idleCtx, idleCancel := context.WithCancelCause(ctx)
	defer idleCancel(nil)
	idleTimer := time.AfterFunc(constants.LLMStreamIdleTimeout, func() {
		idleCancel(fmt.Errorf("stream idle: no event received for %s", constants.LLMStreamIdleTimeout))
	})
	defer idleTimer.Stop()

	resp, err := c.connectStream(ctx, idleCtx, "/v1/messages", body)
	if err != nil {
		c.breaker.recordFailure()
		return nil, err
	}
	defer resp.Body.Close()

	sp := newSSEParser()
	scanner := bufio.NewScanner(resp.Body)
	if err := scanSSE(scanner, sp, onToken, idleTimer); err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			c.breaker.recordFailure()
			return nil, fmt.Errorf("%s: %w", c.cfg.Name, cause)
		}
		c.breaker.recordFailure()
		return nil, fmt.Errorf("%s: read stream: %w", c.cfg.Name, err)
	}
	result := sp.finalize()
	c.breaker.recordSuccess()
	return &result, nil
}

// drainNonOKStream handles a non-200 stream response. It drains the body and
// returns (lastErr, shouldRetry).
func (c *AnthropicClient) drainNonOKStream(resp *http.Response, attempt int) (error, bool) {
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
func (c *AnthropicClient) streamHTTPErr(idleCtx context.Context, err error) error {
	if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return fmt.Errorf("%s: %w", c.cfg.Name, cause)
	}
	return nil
}

// connectAttempt makes one HTTP stream attempt. Returns (resp, nil, false) on
// success, (nil, err, false) on fatal error, (nil, err, true) on retryable error.
func (c *AnthropicClient) connectAttempt(idleCtx context.Context, path string, body []byte, attempt int) (*http.Response, error, bool) {
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
func (c *AnthropicClient) connectStream(ctx, idleCtx context.Context, path string, body []byte) (*http.Response, error) {
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

// toolUseBuilder accumulates tool_use input_json_delta fragments.
type toolUseBuilder struct {
	name string
	id   string
	args bytes.Buffer
}

// Health sends a minimal messages request to verify the API is reachable.
func (c *AnthropicClient) Health(ctx context.Context) error {
	_, err := c.Complete(ctx, &CompletionRequest{
		Model:     c.cfg.HealthModel,
		Messages:  []Message{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	return err
}

// ListModels discovers models via GET /v1/models, including context_window metadata.
func (c *AnthropicClient) ListModels(ctx context.Context) ([]DiscoveredModel, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build models request: %w", c.cfg.Name, err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do models request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read models body: %w", c.cfg.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: models status %d", c.cfg.Name, resp.StatusCode)
	}

	var out anthropicModelsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode models: %w", c.cfg.Name, err)
	}

	models := make([]DiscoveredModel, len(out.Data))
	for i, m := range out.Data {
		models[i] = DiscoveredModel{
			Name:          m.ID,
			ContextWindow: m.ContextWindow,
		}
	}
	return models, nil
}

// setHeaders sets Anthropic-specific HTTP headers.
func (c *AnthropicClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
}

// buildRequest converts a domain CompletionRequest into an Anthropic messages request.
func (c *AnthropicClient) buildRequest(req *CompletionRequest, stream bool) anthropicMessagesRequest {
	// Separate system message from conversation messages.
	var system string
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		msg := anthropicMessage{Role: m.Role}
		msg.Content = c.buildContentBlocks(m)
		messages = append(messages, msg)
	}

	// Map "tool" role to "user" with tool_result content blocks (Anthropic convention).
	for i := range messages {
		if messages[i].Role == "tool" && len(messages[i].Content) > 0 {
			messages[i].Role = "user"
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	anthropicReq := anthropicMessagesRequest{
		Model:     req.Model,
		System:    system,
		Messages:  messages,
		MaxTokens: maxTokens,
		Stream:    stream,
	}

	if len(req.Tools) > 0 {
		anthropicReq.Tools = make([]anthropicToolDef, len(req.Tools))
		for i, t := range req.Tools {
			anthropicReq.Tools[i] = anthropicToolDef{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			}
		}
	}

	return anthropicReq
}

// buildContentBlocks converts a domain Message into Anthropic content blocks.
func (c *AnthropicClient) buildContentBlocks(m Message) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0)

	// Text content.
	if m.Content != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	}

	// Tool calls → tool_use blocks.
	for _, tc := range m.ToolCalls {
		var input map[string]any
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		if input == nil {
			input = map[string]any{}
		}
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	// Tool results → tool_result blocks.
	if m.Role == "tool" && m.Content != "" {
		blocks = append(blocks, anthropicContentBlock{
			Type:      "tool_result",
			ToolUseID: m.ToolCallID,
			Text:      m.Content,
		})
	}

	return blocks
}

// toCompletionResponse converts an Anthropic response to the domain CompletionResponse.
func (c *AnthropicClient) toCompletionResponse(resp *anthropicMessagesResponse) *CompletionResponse {
	result := &CompletionResponse{
		Model: resp.Model,
		Usage: TokenUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			args := "{}"
			if block.Input != nil {
				if b, err := json.Marshal(block.Input); err == nil {
					args = string(b)
				}
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "tool_use",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: block.Name, Arguments: args},
			})
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// AnthropicProtocol — stateless ChatProtocol adapter
// ---------------------------------------------------------------------------

// AnthropicProtocol satisfies ChatProtocol for Anthropic's Messages API.
type AnthropicProtocol struct {
	client   *AnthropicClient
	breakers sync.Map
}

// NewAnthropicProtocol returns a protocol adapter backed by client.
func NewAnthropicProtocol(client *AnthropicClient) *AnthropicProtocol {
	return &AnthropicProtocol{client: client}
}

func (p *AnthropicProtocol) clientFor(cfg ProviderConfig) *AnthropicClient {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.Name+"\x00"+cfg.BaseURL+"\x00"+cfg.APIKey)))
	breaker, _ := p.breakers.LoadOrStore(key, &providerBreaker{state: cbClosed})
	return &AnthropicClient{
		cfg:        cfg,
		http:       p.client.http,
		streamHTTP: p.client.streamHTTP,
		logger:     p.client.logger,
		breaker:    breaker.(*providerBreaker),
	}
}

// Complete implements ChatProtocol.
func (p *AnthropicProtocol) Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
	return p.clientFor(cfg).Complete(ctx, req)
}

// CompleteStream implements ChatProtocol.
func (p *AnthropicProtocol) CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	return p.clientFor(cfg).CompleteStream(ctx, req, onToken)
}

// Health implements ChatProtocol.
func (p *AnthropicProtocol) Health(ctx context.Context, cfg ProviderConfig) error {
	return p.clientFor(cfg).Health(ctx)
}

// ListModels implements ChatProtocol.
func (p *AnthropicProtocol) ListModels(ctx context.Context, cfg ProviderConfig) ([]DiscoveredModel, error) {
	return p.clientFor(cfg).ListModels(ctx)
}

var _ ChatProtocol = (*AnthropicProtocol)(nil)

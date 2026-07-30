package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/internal/platformmcp/requestctx"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

const maxStratumResponseBytes = 64 << 10

type StratumClient struct {
	client    HTTPDoer
	baseURL   *url.URL
	contracts platformmcp.StaticContracts
	metrics   ClientMetrics
}

type ClientMetrics interface {
	IncPlatformMCPTokenExchange(outcome string)
	IncPlatformMCPBackendRequest(toolClass, statusClass string)
	IncPlatformMCPUnknownOutcome(toolClass string)
}

func NewStratumClient(client HTTPDoer, rawBaseURL string) (*StratumClient, error) {
	return NewObservedStratumClient(client, rawBaseURL, noopClientMetrics{})
}

func NewObservedStratumClient(client HTTPDoer, rawBaseURL string, metrics ClientMetrics) (*StratumClient, error) {
	if client == nil {
		return nil, errors.New("Stratum API HTTP client is not configured")
	}
	baseURL, err := parseStratumBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = noopClientMetrics{}
	}
	return &StratumClient{
		client: client, baseURL: baseURL, contracts: platformmcp.NewPhase1Contracts(), metrics: metrics,
	}, nil
}

func (c *StratumClient) Call(
	ctx context.Context,
	toolName string,
	arguments map[string]any,
) (agentport.MCPToolResult, error) {
	contract, ok := c.contracts.Lookup(toolName)
	if !ok {
		return agentport.MCPToolResult{}, fmt.Errorf("call Stratum API: %w", errors.New("unknown tool contract"))
	}
	invocationToken, ok := requestctx.InvocationToken(ctx)
	if !ok {
		return agentport.MCPToolResult{}, errors.New("call Stratum API: invocation token is missing")
	}
	callCtx, cancel := context.WithTimeout(ctx, constants.SystemAssistantToolTimeout)
	defer cancel()
	delegationToken, err := c.exchangeToken(callCtx, invocationToken, resourceID(arguments))
	if err != nil {
		c.metrics.IncPlatformMCPTokenExchange("error")
		return agentport.MCPToolResult{}, err
	}
	c.metrics.IncPlatformMCPTokenExchange("success")
	return c.callDelegated(callCtx, contract, delegationToken, arguments)
}

func (c *StratumClient) exchangeToken(
	ctx context.Context,
	invocationToken, resourceID string,
) (string, error) {
	payload := map[string]string{"invocation_token": invocationToken}
	if resourceID != "" {
		payload["resource_id"] = resourceID
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.postExchange(ctx, payload, &response); err != nil {
		return "", err
	}
	if response.AccessToken == "" {
		return "", errors.New("exchange Platform MCP token: access token is missing")
	}
	return response.AccessToken, nil
}

func (c *StratumClient) postExchange(ctx context.Context, payload, target any) error {
	endpoint := c.resolve("/internal/platform-mcp/token/exchange")
	req, err := newJSONRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return fmt.Errorf("create Platform MCP token exchange request: %w", err)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call Platform MCP token exchange: %w", err)
	}
	if err := decodeSuccessfulResponse(response, target); err != nil {
		return fmt.Errorf("decode Platform MCP token exchange: %w", err)
	}
	return nil
}

func (c *StratumClient) callDelegated(
	ctx context.Context,
	contract platformmcp.ToolContract,
	delegationToken string,
	arguments map[string]any,
) (agentport.MCPToolResult, error) {
	req, err := newJSONRequest(ctx, contract.Method, c.resolve(contract.Path), arguments)
	if err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("create delegated Stratum API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+delegationToken)
	response, err := c.client.Do(req)
	if err != nil {
		class := toolClass(contract.Name)
		c.metrics.IncPlatformMCPBackendRequest(class, transportStatus(err))
		if class == "proposal" {
			c.metrics.IncPlatformMCPUnknownOutcome(class)
		}
		return agentport.MCPToolResult{}, fmt.Errorf("call delegated Stratum API: %w", err)
	}
	c.metrics.IncPlatformMCPBackendRequest(toolClass(contract.Name), httpStatusClass(response.StatusCode))
	var projected map[string]any
	if err := decodeSuccessfulResponse(response, &projected); err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("decode delegated Stratum API response: %w", err)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("project delegated Stratum API response: %w", err)
	}
	return agentport.MCPToolResult{
		Content:           []agentport.MCPContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: projected,
	}, nil
}

func newJSONRequest(ctx context.Context, method, endpoint string, payload any) (*http.Request, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode JSON request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func decodeSuccessfulResponse(response *http.Response, target any) error {
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxStratumResponseBytes))
		return errors.Join(fmt.Errorf("Stratum API returned status %d", response.StatusCode), drainErr)
	}
	limited := io.LimitReader(response.Body, maxStratumResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read Stratum API response: %w", err)
	}
	if len(data) > maxStratumResponseBytes {
		return errors.New("Stratum API response exceeds safe limit")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse Stratum API response: %w", err)
	}
	return nil
}

func parseStratumBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Stratum internal API URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Stratum internal API URL must be a fixed HTTPS origin")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}

func (c *StratumClient) resolve(path string) string {
	resolved := *c.baseURL
	resolved.Path = path
	return resolved.String()
}

func resourceID(arguments map[string]any) string {
	resourceID, _ := arguments["resourceId"].(string)
	return resourceID
}

func toolClass(name string) string {
	switch name {
	case platformmcp.ToolSearchOfficialDocs:
		return "docs"
	case platformmcp.ToolDiagnoseTenant:
		return "diagnostic"
	case platformmcp.ToolProposeResourceChange:
		return "proposal"
	default:
		return "unknown"
	}
}

func httpStatusClass(status int) string {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return "2xx"
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return "4xx"
	case status >= http.StatusInternalServerError:
		return "5xx"
	default:
		return "other"
	}
}

func transportStatus(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "transport_error"
}

type noopClientMetrics struct{}

func (noopClientMetrics) IncPlatformMCPTokenExchange(_ string)     {}
func (noopClientMetrics) IncPlatformMCPBackendRequest(_, _ string) {}
func (noopClientMetrics) IncPlatformMCPUnknownOutcome(_ string)    {}

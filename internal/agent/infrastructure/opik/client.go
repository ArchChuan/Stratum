package opik

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const defaultBatchConcurrency = 4

type Config struct {
	BaseURL   string
	Project   string
	Workspace string
	APIKey    string
	Timeout   time.Duration
}

type Client struct {
	config Config
	http   *http.Client
}

func NewClient(config Config) *Client {
	if config.Timeout <= 0 {
		config.Timeout = constants.DefaultOpikTimeout
	}
	return &Client{config: config, http: &http.Client{Timeout: config.Timeout}}
}

func (c *Client) Resolve(ctx context.Context, tenantID, traceID string) (domain.TraceEvidence, error) {
	trace, err := c.resolveTrace(ctx, tenantID, traceID)
	if err != nil {
		return domain.TraceEvidence{}, err
	}
	var spans spanPage
	if err := c.get(ctx, "/v1/private/spans", url.Values{
		"project_name": {c.config.Project}, "trace_id": {trace.ID}, "page": {"1"}, "size": {"100"},
	}, &spans); err != nil {
		return domain.TraceEvidence{}, err
	}
	return mapEvidence(trace, spans.Content)
}

func (c *Client) ResolveBatch(
	ctx context.Context, tenantID string, traceIDs []string,
) (map[string]domain.TraceEvidence, error) {
	unique := make(map[string]struct{}, len(traceIDs))
	for _, traceID := range traceIDs {
		unique[traceID] = struct{}{}
	}
	if len(unique) == 0 {
		return map[string]domain.TraceEvidence{}, nil
	}

	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	out := make(map[string]domain.TraceEvidence, len(unique))
	var mu sync.Mutex
	var firstErr error
	var workers sync.WaitGroup
	workerCount := min(defaultBatchConcurrency, len(unique))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for traceID := range jobs {
				evidence, err := c.Resolve(batchCtx, tenantID, traceID)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
					cancel()
				} else if err == nil {
					out[traceID] = evidence
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
	}

sendJobs:
	for traceID := range unique {
		select {
		case jobs <- traceID:
		case <-batchCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (c *Client) ToolObservations(
	ctx context.Context, tenantID, traceID string,
) ([]domain.ToolObservation, error) {
	evidence, err := c.Resolve(ctx, tenantID, traceID)
	return evidence.Tools, err
}

func (c *Client) TraceEvents(ctx context.Context, tenantID, traceID string) ([]domain.AgentTraceEvent, error) {
	evidence, err := c.Resolve(ctx, tenantID, traceID)
	return evidence.Events, err
}

func (c *Client) ListExecutions(
	ctx context.Context, tenantID string, opts domain.ListOptions,
) ([]domain.ExecutionRecord, int64, error) {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	filters := traceFilters(tenantID, "")
	var page tracePage
	if err := c.get(ctx, "/v1/private/traces", url.Values{
		"project_name": {c.config.Project}, "page": {strconv.Itoa(opts.Page)}, "size": {strconv.Itoa(opts.PageSize)},
		"filters": {filters}, "sorting": {"[{\"field\":\"start_time\",\"direction\":\"DESC\"}]"},
	}, &page); err != nil {
		return nil, 0, err
	}
	out := make([]domain.ExecutionRecord, 0, len(page.Content))
	for _, trace := range page.Content {
		if metadataString(trace.Metadata, "tenant_id") != tenantID {
			continue
		}
		out = append(out, domain.ExecutionRecord{
			ID: metadataString(trace.Metadata, "execution_id"), TraceID: metadataString(trace.Metadata, "trace_id"),
			AgentID: metadataString(trace.Metadata, "agent_id"), AgentName: metadataString(trace.Metadata, "agent_name"),
			UserID: metadataString(trace.Metadata, "user_id"), Status: metadataString(trace.Metadata, "status"),
			TotalTokens: metadataInt(trace.Metadata, "total_tokens", usageTotal(trace.Usage)),
			CostUSD:     metadataFloat(trace.Metadata, "cost_usd", trace.TotalEstimatedCost),
			DurationMs:  int(metadataInt64(trace.Metadata, "duration_ms", int64(trace.Duration))), CreatedAt: trace.StartTime,
		})
	}
	return out, page.Total, nil
}

func (c *Client) resolveTrace(ctx context.Context, tenantID, traceID string) (opikTrace, error) {
	var page tracePage
	if err := c.get(ctx, "/v1/private/traces", url.Values{
		"project_name": {c.config.Project}, "page": {"1"}, "size": {"2"}, "filters": {traceFilters(tenantID, traceID)},
	}, &page); err != nil {
		return opikTrace{}, err
	}
	for _, trace := range page.Content {
		if metadataString(trace.Metadata, "tenant_id") == tenantID &&
			metadataString(trace.Metadata, "trace_id") == traceID {
			return trace, nil
		}
	}
	return opikTrace{}, domain.ErrEvidenceNotFound
}

func traceFilters(tenantID, traceID string) string {
	filters := []map[string]string{{
		"field": "metadata", "operator": "=", "key": metadataJSONPath("tenant_id"), "value": tenantID,
	}}
	if traceID != "" {
		filters = append(filters, map[string]string{
			"field": "metadata", "operator": "=", "key": metadataJSONPath("trace_id"), "value": traceID,
		})
	}
	encoded, _ := json.Marshal(filters)
	return string(encoded)
}

func metadataJSONPath(key string) string {
	return "$['" + metadataPrefix + key + "']"
}

func (c *Client) get(ctx context.Context, path string, query url.Values, target any) error {
	if strings.TrimSpace(c.config.BaseURL) == "" {
		return domain.ErrEvidenceUnavailable
	}
	endpoint := strings.TrimRight(c.config.BaseURL, "/") + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("opik request: %w", domain.ErrEvidenceInvalid)
	}
	if c.config.Project != "" {
		req.Header.Set("projectName", c.config.Project)
	}
	if c.config.Workspace != "" {
		req.Header.Set("Comet-Workspace", c.config.Workspace)
	}
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", c.config.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("opik request: %w", domain.ErrEvidenceUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return domain.ErrEvidenceNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opik status %d: %w", resp.StatusCode, domain.ErrEvidenceUnavailable)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, constants.MaxOpikResponseBytes+1))
	if err != nil {
		return fmt.Errorf("opik response: %w", domain.ErrEvidenceUnavailable)
	}
	if len(body) > constants.MaxOpikResponseBytes {
		return fmt.Errorf("opik response too large: %w", domain.ErrEvidenceInvalid)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("opik decode: %w", domain.ErrEvidenceInvalid)
	}
	return nil
}

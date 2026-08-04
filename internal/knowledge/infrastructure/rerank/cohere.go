// Package rerank provides external reranker adapters used by RAG retrieval.
// Credentials are platform-level config (config.Config), never tenant-scoped
// model provider credentials.
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/httpclient"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// cohereRerankPath is the v2 rerank endpoint relative to the configured base URL.
const cohereRerankPath = "/v2/rerank"

type cohereRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type cohereRerankResponse struct {
	Results []cohereRerankResult `json:"results"`
}

type cohereRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float32 `json:"relevance_score"`
}

// CohereReranker re-scores retrieval candidates via the Cohere rerank API.
// The endpoint and API key come from platform configuration; the request
// body carries tenant chunk texts to a third party (data governance review).
type CohereReranker struct {
	baseURL string
	apiKey  string
	model   string
	client  httpclient.Doer
	metrics observability.MetricsProvider
	logger  *zap.Logger
}

// NewCohereReranker builds a reranker backed by Cohere v2. doer is injectable
// in tests; production wiring passes httpclient.New configured with the
// RerankHTTPTimeout and RerankHTTPRetryMax budgets.
func NewCohereReranker(baseURL, apiKey, model string, doer httpclient.Doer, metrics observability.MetricsProvider, logger *zap.Logger) *CohereReranker {
	return &CohereReranker{baseURL: baseURL, apiKey: apiKey, model: model, client: doer, metrics: metrics, logger: logger}
}

// Rerank re-scores req.Documents against req.Query, returning results ordered
// by the reranker's own relevance score (highest first).
func (r *CohereReranker) Rerank(ctx context.Context, req port.RerankRequest) ([]port.RerankResult, error) {
	body, err := json.Marshal(cohereRerankRequest{
		Model: r.model, Query: req.Query, Documents: req.Documents, TopN: req.TopN,
	})
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(r.baseURL, "/")+cohereRerankPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	start := time.Now()
	resp, err := r.client.Do(httpReq)
	if err != nil {
		r.record(ctx, "error")
		return nil, fmt.Errorf("rerank: call cohere: %w", err)
	}
	defer resp.Body.Close()
	r.metrics.RecordRerankDuration(r.model, time.Since(start).Seconds())

	if resp.StatusCode != http.StatusOK {
		r.record(ctx, "error")
		return nil, fmt.Errorf("rerank: cohere returned status %d", resp.StatusCode)
	}
	r.record(ctx, "ok")

	var parsed cohereRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		r.record(ctx, "error")
		return nil, fmt.Errorf("rerank: decode response: %w", err)
	}
	results := make([]port.RerankResult, 0, len(parsed.Results))
	for _, res := range parsed.Results {
		results = append(results, port.RerankResult{Index: res.Index, Score: res.RelevanceScore})
	}
	return results, nil
}

func (r *CohereReranker) record(ctx context.Context, status string) {
	r.metrics.IncRerankRequest(reqctx.TenantIDFromContext(ctx), r.model, status)
}

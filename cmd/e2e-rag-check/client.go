package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/golang-jwt/jwt/v5"
)

// infraError marks failures that are the environment's fault (server down,
// auth misconfigured, provider unavailable). run maps these to exit code 2.
type infraError struct{ err error }

func (e *infraError) Error() string { return e.err.Error() }
func (e *infraError) Unwrap() error { return e.err }

func isInfra(err error) bool {
	var infra *infraError
	return errors.As(err, &infra)
}

// httpClient talks to the live Stratum server. It always authenticates as the
// configured tenant owner so the /knowledge/query path resolves the D2 gate
// with an admin-owner exemption (no whitelist shadow over the golden set).
type httpClient struct {
	baseURL    string
	http       *http.Client
	ownerToken string
}

func newHTTPClient(baseURL, ownerToken string) *httpClient {
	return &httpClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		http:       &http.Client{Timeout: DefaultHTTPTimeout},
		ownerToken: ownerToken,
	}
}

// mintOwnerToken signs a tenant owner JWT from JWT_PRIVATE_KEY_PEM. Claim
// names follow internal/iam/application/jwt_service.go jwtAccessClaims.
func mintOwnerToken(tenantID, userID string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_PEM"))
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(userID) == "" || raw == "" {
		return "", errors.New("RAG check requires tenant-id, user-id, and JWT_PRIVATE_KEY_PEM")
	}
	block, _ := pem.Decode([]byte(strings.ReplaceAll(raw, `\n`, "\n")))
	if block == nil {
		return "", errors.New("JWT_PRIVATE_KEY_PEM: no PEM block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("JWT_PRIVATE_KEY_PEM: %w", err)
	}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": userID, "tid": tenantID, "role": "owner", "global_role": "user",
		"system_role": "user", "jti": fmt.Sprintf("rag-check-%d", now.UnixNano()),
		"iat": now.Unix(), "exp": now.Add(DefaultJWTExpiry).Unix(),
	}).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("mint owner token: %w", err)
	}
	return token, nil
}

// classifyHTTP labels a response as infra (server/auth/unavailable) versus a
// defect. 401/403/5xx are infrastructure or identity problems; other 4xx are
// usually a bad request the dataset should never produce.
func classifyHTTP(path string, status int, body string) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status >= 500:
		return &infraError{fmt.Errorf("%s: HTTP %d: %s", path, status, body)}
	default:
		return fmt.Errorf("%s: HTTP %d: %s", path, status, body)
	}
}

// roundtrip performs one authenticated request and returns status plus body.
func (c *httpClient) roundtrip(ctx context.Context, method, path, contentType string, body io.Reader) (int, []byte, error) {
	var requestBody io.Reader
	if body == nil {
		requestBody = http.NoBody
	} else {
		requestBody = body
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody) // #nosec G704 -- base URL is a developer CLI flag, never remote-controlled
	if err != nil {
		return 0, nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.ownerToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req) // #nosec G704 -- base URL is a developer CLI flag, never remote-controlled
	if err != nil {
		return 0, nil, &infraError{fmt.Errorf("%s %s: %w", method, path, err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, &infraError{fmt.Errorf("read %s %s: %w", method, path, err)}
	}
	return resp.StatusCode, data, nil
}

func (c *httpClient) health(ctx context.Context) error {
	status, body, err := c.roundtrip(ctx, http.MethodGet, "/health", "", nil)
	if err != nil {
		return err
	}
	if err := classifyHTTP("/health", status, string(body)); err != nil {
		return err
	}
	return nil
}

// workspaceConfig is the echoed config of a created workspace; it is the
// source of truth for config-drift comparison.
type workspaceConfig struct {
	ID     string `json:"id"`
	Config struct {
		EmbeddingModel string  `json:"embedding_model"`
		TopK           int     `json:"top_k"`
		ScoreThreshold float64 `json:"score_threshold"`
	} `json:"config"`
}

func (c *httpClient) createWorkspace(ctx context.Context, name string, s goldenSnapshot) (*workspaceConfig, error) {
	payload, err := json.Marshal(map[string]any{
		"name":        name,
		"description": "transient e2e-rag-check workspace (cleaned up after run)",
		"config": map[string]any{
			"embedding_model":   s.EmbeddingModel,
			"chunking_strategy": knowledgedomain.DefaultChunkingStrategy,
			"chunk_size":        s.ChunkSize,
			"chunk_overlap":     s.ChunkOverlap,
			"query_mode":        s.QueryMode,
			"top_k":             s.TopK,
			"reranking":         s.Reranking,
			"score_threshold":   s.ScoreThreshold,
			"rerank_top_k":      0,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode create workspace: %w", err)
	}
	status, body, err := c.roundtrip(ctx, http.MethodPost, "/knowledge/workspaces", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if err := classifyHTTP("/knowledge/workspaces", status, string(body)); err != nil {
		return nil, err
	}
	var created workspaceConfig
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, &infraError{fmt.Errorf("decode create workspace response: %w", err)}
	}
	if created.ID == "" {
		return nil, &infraError{errors.New("create workspace response missing id")}
	}
	return &created, nil
}

func (c *httpClient) deleteWorkspace(ctx context.Context, name string) error {
	status, body, err := c.roundtrip(ctx, http.MethodDelete, "/knowledge/workspaces/"+name, "", nil)
	if err != nil {
		return err
	}
	return classifyHTTP("/knowledge/workspaces/"+name, status, string(body))
}

func (c *httpClient) ingest(ctx context.Context, workspace, source string, data []byte) error {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err := writer.WriteField("workspace", workspace); err != nil {
		return fmt.Errorf("ingest form: %w", err)
	}
	file, err := writer.CreateFormFile("file", source)
	if err != nil {
		return fmt.Errorf("ingest form: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("ingest form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("ingest form: %w", err)
	}
	status, body, err := c.roundtrip(ctx, http.MethodPost, "/knowledge/ingest", writer.FormDataContentType(), &form)
	if err != nil {
		return err
	}
	return classifyHTTP("/knowledge/ingest", status, string(body))
}

type document struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	IngestStatus    string `json:"ingest_status"`
	IngestError     string `json:"ingest_error"`
	ProcessedChunks int    `json:"processed_chunks"`
}

func (c *httpClient) listDocuments(ctx context.Context, workspace string) ([]document, error) {
	status, body, err := c.roundtrip(ctx, http.MethodGet, "/knowledge/workspaces/"+workspace+"/documents", "", nil)
	if err != nil {
		return nil, err
	}
	if err := classifyHTTP("/knowledge/workspaces/"+workspace+"/documents", status, string(body)); err != nil {
		return nil, err
	}
	var payload struct {
		Documents []document `json:"documents"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &infraError{fmt.Errorf("decode documents list: %w", err)}
	}
	return payload.Documents, nil
}

// ingestState is the per-poll classification of the expected sources: which
// are missing, which failed, and whether any are still processing. allReady
// means every expected source has completed with processed chunks.
type ingestState struct {
	missing  []string
	failed   []string
	pending  bool
	allReady bool
}

// classifyDocument records one document's terminal/transitional status into
// the poll state. A zero-chunk completion is treated as a failure: the ingest
// job finished without producing retrievable content.
func classifyDocument(source string, doc document, state *ingestState) {
	switch doc.IngestStatus {
	case constants.IngestStatusProcessing:
		state.pending = true
		state.allReady = false
	case constants.IngestStatusFailed:
		state.failed = append(state.failed, fmt.Sprintf("%s (%s)", source, doc.IngestError))
	case constants.IngestStatusCompleted:
		if doc.ProcessedChunks <= 0 {
			state.failed = append(state.failed, fmt.Sprintf("%s (completed with 0 chunks)", source))
		}
	}
}

// classifyIngest maps the current documents list onto the expected sources.
func classifyIngest(docs []document, expected []string) ingestState {
	bySource := make(map[string]document, len(docs))
	for _, doc := range docs {
		bySource[doc.Source] = doc
	}
	state := ingestState{allReady: true}
	for _, source := range expected {
		doc, ok := bySource[source]
		if !ok {
			state.missing = append(state.missing, source)
			state.allReady = false
			continue
		}
		classifyDocument(source, doc, &state)
	}
	return state
}

// waitForIngest polls the documents list until every expected source reaches a
// terminal state. Completed docs must have processed chunks; failures abort.
func (c *httpClient) waitForIngest(ctx context.Context, workspace string, expected []string) ([]document, error) {
	deadline := time.Now().Add(DefaultIngestPollTimeout)
	for {
		docs, err := c.listDocuments(ctx, workspace)
		if err != nil {
			return nil, err
		}
		state := classifyIngest(docs, expected)
		if len(state.failed) > 0 {
			return nil, fmt.Errorf("ingest failed: %s", strings.Join(state.failed, "; "))
		}
		if state.allReady {
			return docs, nil
		}
		if time.Now().After(deadline) {
			return nil, &infraError{fmt.Errorf("ingest poll timeout after %s (missing=%v pending=%v)",
				DefaultIngestPollTimeout, state.missing, state.pending)}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(DefaultIngestPollEvery):
		}
	}
}

// indexDocuments builds the source->document-id map for completed docs.
func indexDocuments(docs []document) (map[string]string, error) {
	index := make(map[string]string)
	for _, doc := range docs {
		if doc.IngestStatus != constants.IngestStatusCompleted {
			continue
		}
		if prior, ok := index[doc.Source]; ok && prior != doc.ID {
			return nil, fmt.Errorf("source %q maps to multiple document ids (%s, %s)", doc.Source, prior, doc.ID)
		}
		index[doc.Source] = doc.ID
	}
	return index, nil
}

type queryResponse struct {
	Sources []struct {
		DocumentID string  `json:"document_id"`
		Content    string  `json:"content"`
		Score      float32 `json:"score"`
	} `json:"sources"`
	BestScore float32 `json:"best_score"`
}

// httpEvaluationRetriever implements knowledgeapp.EvaluationRetriever over the
// real HTTP /knowledge/query path. It deliberately ignores ViewerID and
// SkipAccessCheck: every call carries the owner JWT, so the server resolves the
// D2 gate with an admin-owner exemption and the tool measures the real access
// path rather than the worker-internal bypass. It maps only the fields the
// HTTP contract serves (reranking and query_rewrite are pinned to the identity
// values by the dataset).
type httpEvaluationRetriever struct {
	client *httpClient
}

func (r *httpEvaluationRetriever) Query(ctx context.Context, request knowledgeapp.RAGQueryRequest) (*knowledgeapp.RAGQueryResult, error) {
	payload, err := json.Marshal(map[string]any{
		"question":  request.Question,
		"workspace": request.Workspace,
		"mode":      request.Mode,
		"topK":      request.TopK,
	})
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}
	status, body, err := r.client.roundtrip(ctx, http.MethodPost, "/knowledge/query", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if err := classifyHTTP("/knowledge/query", status, string(body)); err != nil {
		return nil, err
	}
	var response queryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, &infraError{fmt.Errorf("decode query response: %w", err)}
	}
	result := &knowledgeapp.RAGQueryResult{
		Sources:   make([]knowledgeapp.Source, 0, len(response.Sources)),
		BestScore: response.BestScore,
	}
	for _, source := range response.Sources {
		result.Sources = append(result.Sources, knowledgeapp.Source{
			DocumentID: source.DocumentID,
			Content:    source.Content,
			Score:      source.Score,
		})
	}
	return result, nil
}

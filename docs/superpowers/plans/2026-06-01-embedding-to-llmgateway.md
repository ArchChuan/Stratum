# Embedding 包迁移至 LLMGateway 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `internal/embedding` 包中对 `openai.Client` 的直接调用，替换为通过 `internal/llmgateway` 包的统一接口调用。

**Architecture:** 在 `llmgateway` 中新增 `EmbeddingClient` 接口和对应的 OpenAI 实现，`OpenAIClient` 实现该接口；`EmbeddingService` 的构造函数改为接收 `llmgateway.EmbeddingClient`，对外公开 API（`EmbedVector`、`EmbedBatch`、`GetVectorDimension`）保持不变，调用方无需修改。

**Tech Stack:** Go 1.24, `internal/llmgateway`（自研网关），`go.uber.org/zap`，标准库 `net/http`

---

## 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/llmgateway/gateway.go` | 修改 | 新增 `EmbeddingRequest`、`EmbeddingResponse`、`EmbeddingClient` 接口；`Gateway` 增加 embedding 路由 |
| `internal/llmgateway/openai.go` | 修改 | `OpenAIClient` 实现 `EmbeddingClient` 接口，新增 `CreateEmbeddings` 方法 |
| `internal/llmgateway/config.go` | 修改 | `InitializeGateway` 注册 OpenAI embedding 客户端 |
| `internal/embedding/embedding.go` | 修改 | 替换 `*openai.Client` 为 `llmgateway.EmbeddingClient`，更新构造函数签名 |
| `internal/embedding/embedding_test.go` | 修改 | 使用 mock `EmbeddingClient` 替代真实 API key |

---

### Task 1: 在 llmgateway 中定义 EmbeddingClient 接口

**Files:**
- Modify: `internal/llmgateway/gateway.go`

- [ ] **Step 1: 在 `gateway.go` 末尾追加 embedding 相关类型和接口**

在 `gateway.go` 的 `LLMClient` 接口定义之后，追加以下内容：

```go
type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type EmbeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type EmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}
```

同时在 `Gateway` 结构体中增加 `embeddingClients` 字段，并添加注册和调用方法：

完整替换 `gateway.go` 内容如下：

```go
package llmgateway

import (
	"context"
	"fmt"
)

type ModelProvider string

const (
	ProviderOpenAI ModelProvider = "openai"
	ProviderClaude ModelProvider = "claude"
	ProviderGemini ModelProvider = "gemini"
	ProviderOllama ModelProvider = "ollama"
	ProviderLLaMA  ModelProvider = "llama"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	TopP        float32   `json:"top_p,omitempty"`
}

type CompletionResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type LLMClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Health(ctx context.Context) error
}

type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type EmbeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type EmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

type Gateway struct {
	clients          map[ModelProvider]LLMClient
	embeddingClients map[ModelProvider]EmbeddingClient
	defaultProvider  ModelProvider
}

func NewGateway() *Gateway {
	return &Gateway{
		clients:          make(map[ModelProvider]LLMClient),
		embeddingClients: make(map[ModelProvider]EmbeddingClient),
	}
}

func (g *Gateway) RegisterClient(provider ModelProvider, client LLMClient) {
	g.clients[provider] = client
}

func (g *Gateway) RegisterEmbeddingClient(provider ModelProvider, client EmbeddingClient) {
	g.embeddingClients[provider] = client
}

func (g *Gateway) SetDefault(provider ModelProvider) {
	g.defaultProvider = provider
}

func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	provider := g.defaultProvider
	if req.Model != "" {
		provider = g.parseProvider(req.Model)
	}

	client, ok := g.clients[provider]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", provider)
	}

	return client.Complete(ctx, req)
}

func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	client, ok := g.embeddingClients[g.defaultProvider]
	if !ok {
		// 回退到 OpenAI
		client, ok = g.embeddingClients[ProviderOpenAI]
		if !ok {
			return nil, fmt.Errorf("no embedding client registered")
		}
	}
	return client.CreateEmbeddings(ctx, req)
}

func (g *Gateway) Health(ctx context.Context) error {
	for provider, client := range g.clients {
		if err := client.Health(ctx); err != nil {
			return fmt.Errorf("provider %s health check failed: %w", provider, err)
		}
	}
	return nil
}

func (g *Gateway) parseProvider(model string) ModelProvider {
	switch model {
	case "gpt-4", "gpt-3.5-turbo":
		return ProviderOpenAI
	case "claude-3-opus", "claude-3-sonnet":
		return ProviderClaude
	case "gemini-pro":
		return ProviderGemini
	case "ollama":
		return ProviderOllama
	default:
		return g.defaultProvider
	}
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go build ./internal/llmgateway/...
```

期望输出：无错误

- [ ] **Step 3: Commit**

```bash
git add internal/llmgateway/gateway.go
git commit -m "feat(llmgateway): add EmbeddingClient interface and Gateway embedding routing"
```

---

### Task 2: OpenAIClient 实现 EmbeddingClient 接口

**Files:**
- Modify: `internal/llmgateway/openai.go`

- [ ] **Step 1: 在 `openai.go` 中为 `OpenAIClient` 添加 `CreateEmbeddings` 方法**

在文件末尾（`Health` 方法之后）追加：

```go
func (c *OpenAIClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	openaiReq := map[string]interface{}{
		"input": req.Input,
		"model": model,
	}

	body, err := json.Marshal(openaiReq)
	if err != nil {
		c.logger.Error("failed to marshal embedding request", zap.Error(err))
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		c.logger.Error("failed to create embedding request", zap.Error(err))
		return nil, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		c.logger.Error("failed to call OpenAI embeddings API", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		c.logger.Error("OpenAI embeddings API error",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)))
		return nil, fmt.Errorf("OpenAI embeddings API error: %d", resp.StatusCode)
	}

	var openaiResp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		c.logger.Error("failed to decode embedding response", zap.Error(err))
		return nil, err
	}

	result := &EmbeddingResponse{
		Embeddings: make([][]float32, len(openaiResp.Data)),
	}
	for i, d := range openaiResp.Data {
		result.Embeddings[i] = d.Embedding
	}

	c.logger.Info("OpenAI embeddings success",
		zap.String("model", model),
		zap.Int("count", len(result.Embeddings)))
	return result, nil
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go build ./internal/llmgateway/...
```

期望输出：无错误

- [ ] **Step 3: Commit**

```bash
git add internal/llmgateway/openai.go
git commit -m "feat(llmgateway): OpenAIClient implements EmbeddingClient interface"
```

---

### Task 3: InitializeGateway 注册 embedding 客户端

**Files:**
- Modify: `internal/llmgateway/config.go`

- [ ] **Step 1: 在 `InitializeGateway` 中注册 OpenAI embedding 客户端**

将 `config.go` 中注册 OpenAI 客户端的代码块修改为同时注册 embedding 客户端：

```go
// 注册 OpenAI 客户端
if cfg.OpenAIKey != "" {
    openaiClient := NewOpenAIClient(cfg.OpenAIKey, "", logger)
    gateway.RegisterClient(ProviderOpenAI, openaiClient)
    gateway.RegisterEmbeddingClient(ProviderOpenAI, openaiClient)
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go build ./internal/llmgateway/...
```

期望输出：无错误

- [ ] **Step 3: Commit**

```bash
git add internal/llmgateway/config.go
git commit -m "feat(llmgateway): register OpenAI as embedding client in InitializeGateway"
```

---

### Task 4: 重构 EmbeddingService 使用 llmgateway.EmbeddingClient

**Files:**
- Modify: `internal/embedding/embedding.go`

- [ ] **Step 1: 替换 embedding.go 全部内容**

```go
package embedding

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

type EmbeddingService struct {
	client llmgateway.EmbeddingClient
	model  string
	logger *zap.Logger
}

func NewEmbeddingService(client llmgateway.EmbeddingClient, logger *zap.Logger) *EmbeddingService {
	return &EmbeddingService{
		client: client,
		model:  "text-embedding-3-small",
		logger: logger,
	}
}

func (e *EmbeddingService) EmbedVector(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.client.CreateEmbeddings(ctx, &llmgateway.EmbeddingRequest{
		Input: []string{text},
		Model: e.model,
	})
	if err != nil {
		e.logger.Error("failed to create embedding", zap.Error(err))
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return resp.Embeddings[0], nil
}

func (e *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	const batchSize = 100
	var allVectors [][]float32

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch := texts[i:end]
		resp, err := e.client.CreateEmbeddings(ctx, &llmgateway.EmbeddingRequest{
			Input: batch,
			Model: e.model,
		})
		if err != nil {
			e.logger.Error("failed to create batch embeddings",
				zap.Int("batch_start", i),
				zap.Int("batch_end", end),
				zap.Error(err))
			return nil, fmt.Errorf("failed to create batch embeddings: %w", err)
		}

		allVectors = append(allVectors, resp.Embeddings...)
	}

	return allVectors, nil
}

func (e *EmbeddingService) GetVectorDimension() int {
	return 1536
}
```

- [ ] **Step 2: 编译验证**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go build ./internal/embedding/...
```

期望输出：无错误

- [ ] **Step 3: Commit**

```bash
git add internal/embedding/embedding.go
git commit -m "refactor(embedding): replace openai.Client with llmgateway.EmbeddingClient"
```

---

### Task 5: 更新 embedding 测试

**Files:**
- Modify: `internal/embedding/embedding_test.go`

- [ ] **Step 1: 替换 embedding_test.go 全部内容，使用 mock EmbeddingClient**

```go
package embedding

import (
	"context"
	"fmt"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

type mockEmbeddingClient struct {
	embeddings [][]float32
	err        error
}

func (m *mockEmbeddingClient) CreateEmbeddings(_ context.Context, req *llmgateway.EmbeddingRequest) (*llmgateway.EmbeddingResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([][]float32, len(req.Input))
	for i := range req.Input {
		if i < len(m.embeddings) {
			result[i] = m.embeddings[i]
		} else {
			result[i] = []float32{0.1, 0.2, 0.3}
		}
	}
	return &llmgateway.EmbeddingResponse{Embeddings: result}, nil
}

func TestNewEmbeddingService(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{}
	service := NewEmbeddingService(mock, logger)

	if service == nil {
		t.Error("expected service to be non-nil")
	}
	if service.client == nil {
		t.Error("expected client to be non-nil")
	}
	if service.logger == nil {
		t.Error("expected logger to be non-nil")
	}
}

func TestEmbedVector(t *testing.T) {
	logger := zap.NewNop()
	want := []float32{0.1, 0.2, 0.3}
	mock := &mockEmbeddingClient{embeddings: [][]float32{want}}
	service := NewEmbeddingService(mock, logger)

	got, err := service.EmbedVector(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d dims, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dim %d: want %f, got %f", i, want[i], got[i])
		}
	}
}

func TestEmbedVectorError(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{err: fmt.Errorf("api error")}
	service := NewEmbeddingService(mock, logger)

	_, err := service.EmbedVector(context.Background(), "hello")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestEmbedBatch(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{}
	service := NewEmbeddingService(mock, logger)

	texts := []string{"a", "b", "c"}
	got, err := service.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(got))
	}
}

func TestGetVectorDimension(t *testing.T) {
	service := NewEmbeddingService(&mockEmbeddingClient{}, zap.NewNop())
	if service.GetVectorDimension() != 1536 {
		t.Errorf("expected 1536, got %d", service.GetVectorDimension())
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go test ./internal/embedding/... -v
```

期望输出：所有测试 PASS

- [ ] **Step 3: Commit**

```bash
git add internal/embedding/embedding_test.go
git commit -m "test(embedding): use mock EmbeddingClient, add EmbedVector/EmbedBatch tests"
```

---

### Task 6: 全量编译和测试验证

**Files:** 无新增文件

- [ ] **Step 1: 全量编译**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go build ./...
```

期望输出：无错误

- [ ] **Step 2: go vet**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go vet ./...
```

期望输出：无警告

- [ ] **Step 3: 运行所有短测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go && go test -short ./...
```

期望输出：所有测试 PASS（或 SKIP，无 FAIL）

- [ ] **Step 4: Commit（如有遗漏文件）**

若前面步骤有遗漏，在此统一提交：

```bash
git add -p
git commit -m "chore: finalize embedding-to-llmgateway migration"
```

---

## 自检清单

- [x] **Spec 覆盖**：embedding 包所有模型调用（`EmbedVector`、`EmbedBatch`）均通过 `llmgateway.EmbeddingClient` 路由
- [x] **无占位符**：所有步骤包含完整可运行代码
- [x] **类型一致性**：`EmbeddingRequest`/`EmbeddingResponse` 在 Task 1 定义，Task 2/4/5 均引用同一类型
- [x] **调用方兼容**：`EmbeddingService` 公开 API 不变，`rag_service.go` 和 `knowledge_ingest.go` 无需修改（构造函数签名变化，但这两个文件不调用 `NewEmbeddingService`，由 `main.go` 或 DI 层负责）
- [x] **旧依赖清理**：`embedding.go` 不再 import `github.com/sashabaranov/go-openai`

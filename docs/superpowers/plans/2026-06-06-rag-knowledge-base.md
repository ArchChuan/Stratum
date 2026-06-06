# RAG 知识库构建与配置管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 替换 LLM Gateway（Qwen + Zhipu）+ 实现租户内 RAG workspace 持久化管理（CRUD + 配置）+ 前端知识库管理页面。

**Architecture:** LLM gateway 用 model name 前缀路由到 Qwen / Zhipu（均 OpenAI-compatible HTTP）；workspace 元数据持久化到租户 PG schema（`rag_workspaces` 表，追加到 `tenant_schema.sql`）；RAGHandler 加 `db *pgxpool.Pool` 依赖，执行完整 CRUD；前端两页：列表 + 详情。

**Tech Stack:** Go 1.21 · gin · pgx/v5 · Milvus · React 18 · Ant Design 5 · Vite

---

## Task 1: LLM Gateway — 删除旧 provider 文件

**Files:**
- Delete: `internal/llmgateway/openai.go`
- Delete: `internal/llmgateway/anthropic.go`
- Delete: `internal/llmgateway/ollama.go`

- [ ] **Step 1: 确认这三个文件当前内容，确认无其他包引用**

```bash
grep -r "NewOpenAIClient\|NewAnthropicClient\|NewOllamaClient" \
  --include="*.go" /home/yang/go-projects/ClawHermes-AI-Go/
```

Expected: 只在 `internal/llmgateway/config.go:InitializeGateway` 引用。

- [ ] **Step 2: 删除三个文件**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
rm internal/llmgateway/openai.go internal/llmgateway/anthropic.go internal/llmgateway/ollama.go
```

- [ ] **Step 3: 验证编译错误（预期，因为 config.go 还引用这些函数）**

```bash
go build ./internal/llmgateway/...
```

Expected: 编译错误（missing functions），Task 2 修复。

---

## Task 2: LLM Gateway — gateway.go provider 常量和路由

**Files:**
- Modify: `internal/llmgateway/gateway.go`

- [ ] **Step 1: 替换 provider 常量**

在 `internal/llmgateway/gateway.go` 中，将：

```go
const (
    ProviderOpenAI ModelProvider = "openai"
    ProviderClaude ModelProvider = "claude"
    ProviderGemini ModelProvider = "gemini"
    ProviderOllama ModelProvider = "ollama"
    ProviderLLaMA  ModelProvider = "llama"
)
```

替换为：

```go
const (
    ProviderQwen  ModelProvider = "qwen"
    ProviderZhipu ModelProvider = "zhipu"
)
```

- [ ] **Step 2: 替换 parseProvider 函数**

将：

```go
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

替换为：

```go
func (g *Gateway) parseProvider(model string) ModelProvider {
    switch {
    case strings.HasPrefix(model, "text-embedding-v3"), strings.HasPrefix(model, "qwen-"):
        return ProviderQwen
    case strings.HasPrefix(model, "embedding-3"), strings.HasPrefix(model, "glm-"):
        return ProviderZhipu
    default:
        return g.defaultProvider
    }
}
```

- [ ] **Step 3: 在 `CreateEmbeddings` 中加 model-based routing**

当前 `CreateEmbeddings` 只用 `g.defaultProvider`，改为也走 model routing：

将：

```go
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
    client, ok := g.embeddingClients[g.defaultProvider]
    if !ok {
```

替换为：

```go
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
    provider := g.defaultProvider
    if req.Model != "" {
        provider = g.parseProvider(req.Model)
    }
    client, ok := g.embeddingClients[provider]
    if !ok {
```

- [ ] **Step 4: 添加 `strings` import**

在 import 块中加入 `"strings"`。

- [ ] **Step 5: 运行编译（预期仍有错误，因 config.go 还未更新）**

```bash
go build ./internal/llmgateway/...
```

---

## Task 3: LLM Gateway — 新建 qwen.go + zhipu.go

**Files:**
- Create: `internal/llmgateway/qwen.go`
- Create: `internal/llmgateway/zhipu.go`

- [ ] **Step 1: 创建 qwen.go**

```go
package llmgateway

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "go.uber.org/zap"
)

const qwenBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

type QwenClient struct {
    apiKey string
    base   string
    http   *http.Client
    logger *zap.Logger
}

func NewQwenClient(apiKey string, logger *zap.Logger) *QwenClient {
    return &QwenClient{
        apiKey: apiKey,
        base:   qwenBaseURL,
        http:   &http.Client{Timeout: 60 * time.Second},
        logger: logger,
    }
}

func (c *QwenClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("qwen: marshal request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("qwen: build request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("qwen: do request: %w", err)
    }
    defer resp.Body.Close() //nolint:errcheck

    raw, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("qwen: read body: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("qwen: status %d: %s", resp.StatusCode, string(raw))
    }

    var out struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
        Model string `json:"model"`
        Usage struct {
            PromptTokens     int `json:"prompt_tokens"`
            CompletionTokens int `json:"completion_tokens"`
            TotalTokens      int `json:"total_tokens"`
        } `json:"usage"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("qwen: decode response: %w", err)
    }
    if len(out.Choices) == 0 {
        return nil, fmt.Errorf("qwen: no choices in response")
    }

    return &CompletionResponse{
        Content: out.Choices[0].Message.Content,
        Model:   out.Model,
        Usage: struct {
            PromptTokens     int `json:"prompt_tokens"`
            CompletionTokens int `json:"completion_tokens"`
            TotalTokens      int `json:"total_tokens"`
        }{
            PromptTokens:     out.Usage.PromptTokens,
            CompletionTokens: out.Usage.CompletionTokens,
            TotalTokens:      out.Usage.TotalTokens,
        },
    }, nil
}

func (c *QwenClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
    body, err := json.Marshal(map[string]any{
        "model": req.Model,
        "input": req.Input,
    })
    if err != nil {
        return nil, fmt.Errorf("qwen: marshal embed request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("qwen: build embed request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("qwen: do embed request: %w", err)
    }
    defer resp.Body.Close() //nolint:errcheck

    raw, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("qwen: read embed body: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("qwen: embed status %d: %s", resp.StatusCode, string(raw))
    }

    var out struct {
        Data []struct {
            Embedding []float32 `json:"embedding"`
        } `json:"data"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("qwen: decode embed response: %w", err)
    }

    embeddings := make([][]float32, len(out.Data))
    for i, d := range out.Data {
        embeddings[i] = d.Embedding
    }
    return &EmbeddingResponse{Embeddings: embeddings}, nil
}

func (c *QwenClient) Health(ctx context.Context) error {
    _, err := c.Complete(ctx, &CompletionRequest{
        Model:     "qwen-turbo",
        Messages:  []Message{{Role: "user", Content: "ping"}},
        MaxTokens: 1,
    })
    return err
}
```

- [ ] **Step 2: 创建 zhipu.go**

```go
package llmgateway

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "go.uber.org/zap"
)

const zhipuBaseURL = "https://open.bigmodel.cn/api/paas/v4"

type ZhipuClient struct {
    apiKey string
    base   string
    http   *http.Client
    logger *zap.Logger
}

func NewZhipuClient(apiKey string, logger *zap.Logger) *ZhipuClient {
    return &ZhipuClient{
        apiKey: apiKey,
        base:   zhipuBaseURL,
        http:   &http.Client{Timeout: 60 * time.Second},
        logger: logger,
    }
}

func (c *ZhipuClient) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("zhipu: marshal request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("zhipu: build request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("zhipu: do request: %w", err)
    }
    defer resp.Body.Close() //nolint:errcheck

    raw, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("zhipu: read body: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("zhipu: status %d: %s", resp.StatusCode, string(raw))
    }

    var out struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
        Model string `json:"model"`
        Usage struct {
            PromptTokens     int `json:"prompt_tokens"`
            CompletionTokens int `json:"completion_tokens"`
            TotalTokens      int `json:"total_tokens"`
        } `json:"usage"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("zhipu: decode response: %w", err)
    }
    if len(out.Choices) == 0 {
        return nil, fmt.Errorf("zhipu: no choices in response")
    }

    return &CompletionResponse{
        Content: out.Choices[0].Message.Content,
        Model:   out.Model,
        Usage: struct {
            PromptTokens     int `json:"prompt_tokens"`
            CompletionTokens int `json:"completion_tokens"`
            TotalTokens      int `json:"total_tokens"`
        }{
            PromptTokens:     out.Usage.PromptTokens,
            CompletionTokens: out.Usage.CompletionTokens,
            TotalTokens:      out.Usage.TotalTokens,
        },
    }, nil
}

func (c *ZhipuClient) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
    body, err := json.Marshal(map[string]any{
        "model": req.Model,
        "input": req.Input,
    })
    if err != nil {
        return nil, fmt.Errorf("zhipu: marshal embed request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("zhipu: build embed request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("zhipu: do embed request: %w", err)
    }
    defer resp.Body.Close() //nolint:errcheck

    raw, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("zhipu: read embed body: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("zhipu: embed status %d: %s", resp.StatusCode, string(raw))
    }

    var out struct {
        Data []struct {
            Embedding []float32 `json:"embedding"`
        } `json:"data"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("zhipu: decode embed response: %w", err)
    }

    embeddings := make([][]float32, len(out.Data))
    for i, d := range out.Data {
        embeddings[i] = d.Embedding
    }
    return &EmbeddingResponse{Embeddings: embeddings}, nil
}

func (c *ZhipuClient) Health(ctx context.Context) error {
    _, err := c.Complete(ctx, &CompletionRequest{
        Model:     "glm-4-flash",
        Messages:  []Message{{Role: "user", Content: "ping"}},
        MaxTokens: 1,
    })
    return err
}
```

---

## Task 4: LLM Gateway — 更新 config.go 和 internal/config/config.go

**Files:**
- Modify: `internal/llmgateway/config.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: 替换 internal/llmgateway/config.go 全文**

```go
package llmgateway

import (
    "os"

    "go.uber.org/zap"
)

type Config struct {
    QwenAPIKey      string
    ZhipuAPIKey     string
    DefaultProvider ModelProvider
}

func LoadConfig() *Config {
    return &Config{
        QwenAPIKey:      os.Getenv("QWEN_API_KEY"),
        ZhipuAPIKey:     os.Getenv("ZHIPU_API_KEY"),
        DefaultProvider: ProviderQwen,
    }
}

func InitializeGateway(cfg *Config, logger *zap.Logger) *Gateway {
    gateway := NewGateway()

    if cfg.QwenAPIKey != "" {
        qwenClient := NewQwenClient(cfg.QwenAPIKey, logger)
        gateway.RegisterClient(ProviderQwen, qwenClient)
        gateway.RegisterEmbeddingClient(ProviderQwen, qwenClient)
    }

    if cfg.ZhipuAPIKey != "" {
        zhipuClient := NewZhipuClient(cfg.ZhipuAPIKey, logger)
        gateway.RegisterClient(ProviderZhipu, zhipuClient)
        gateway.RegisterEmbeddingClient(ProviderZhipu, zhipuClient)
    }

    if cfg.DefaultProvider != "" {
        gateway.SetDefault(cfg.DefaultProvider)
    } else {
        gateway.SetDefault(ProviderQwen)
    }

    return gateway
}
```

- [ ] **Step 2: 在 internal/config/config.go 中替换 OpenAIAPIKey 字段**

将 `Config` 结构体中：
```go
    OpenAIAPIKey           string
```
替换为：
```go
    QwenAPIKey  string
    ZhipuAPIKey string
```

将 `Load()` 函数中：
```go
        OpenAIAPIKey:       getEnv("OPENAI_API_KEY", ""),
```
替换为：
```go
        QwenAPIKey:  getEnv("QWEN_API_KEY", ""),
        ZhipuAPIKey: getEnv("ZHIPU_API_KEY", ""),
```

- [ ] **Step 3: 检查 router.go 是否引用 OpenAIAPIKey**

```bash
grep -n "OpenAIAPIKey" /home/yang/go-projects/ClawHermes-AI-Go/api/router.go
```

Expected: 无引用（gateway 由 main.go 初始化，router.go 接收 *Gateway 参数）。

- [ ] **Step 4: 检查 main.go 或 cmd/ 中 gateway 初始化**

```bash
grep -rn "OpenAIAPIKey\|InitializeGateway\|LoadConfig" \
  --include="*.go" /home/yang/go-projects/ClawHermes-AI-Go/ | grep -v "_test.go"
```

- [ ] **Step 5: 编译整个 llmgateway 包**

```bash
go build ./internal/llmgateway/... && go build ./internal/config/... && go build ./...
```

Expected: 编译成功（如有 main.go 引用 OpenAIAPIKey，按 Step 4 结果修复）。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add internal/llmgateway/ internal/config/config.go
git commit -m "feat(llmgateway): replace OpenAI/Anthropic/Ollama with Qwen and Zhipu providers"
```

---

## Task 5: LLM Gateway — 单元测试 qwen_test.go + zhipu_test.go

**Files:**
- Create: `internal/llmgateway/qwen_test.go`
- Create: `internal/llmgateway/zhipu_test.go`

- [ ] **Step 1: 创建 qwen_test.go**

```go
package llmgateway_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
    "go.uber.org/zap"
)

func TestQwenClient_Complete(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/chat/completions" {
            t.Errorf("unexpected path: %s", r.URL.Path)
        }
        if r.Header.Get("Authorization") != "Bearer test-key" {
            t.Errorf("missing auth header")
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
            "choices": []map[string]any{
                {"message": map[string]string{"content": "hello"}},
            },
            "model": "qwen-turbo",
            "usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
        })
    }))
    defer srv.Close()

    client := llmgateway.NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
    resp, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
        Model:    "qwen-turbo",
        Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if resp.Content != "hello" {
        t.Errorf("want 'hello', got %q", resp.Content)
    }
}

func TestQwenClient_CreateEmbeddings(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
            "data": []map[string]any{
                {"embedding": []float32{0.1, 0.2, 0.3}},
            },
        })
    }))
    defer srv.Close()

    client := llmgateway.NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
    resp, err := client.CreateEmbeddings(context.Background(), &llmgateway.EmbeddingRequest{
        Model: "text-embedding-v3",
        Input: []string{"hello"},
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 3 {
        t.Errorf("unexpected embeddings: %v", resp.Embeddings)
    }
}

func TestQwenClient_ErrorStatus(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusUnauthorized)
        w.Write([]byte(`{"error":"invalid api key"}`)) //nolint:errcheck
    }))
    defer srv.Close()

    client := llmgateway.NewQwenClientWithBase("bad-key", srv.URL, zap.NewNop())
    _, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
        Model:    "qwen-turbo",
        Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
    })
    if err == nil {
        t.Error("expected error, got nil")
    }
}
```

- [ ] **Step 2: 创建 zhipu_test.go（结构相同，换 Zhipu）**

```go
package llmgateway_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
    "go.uber.org/zap"
)

func TestZhipuClient_Complete(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/chat/completions" {
            t.Errorf("unexpected path: %s", r.URL.Path)
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
            "choices": []map[string]any{
                {"message": map[string]string{"content": "world"}},
            },
            "model": "glm-4-flash",
            "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
        })
    }))
    defer srv.Close()

    client := llmgateway.NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
    resp, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
        Model:    "glm-4-flash",
        Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if resp.Content != "world" {
        t.Errorf("want 'world', got %q", resp.Content)
    }
}

func TestZhipuClient_CreateEmbeddings(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
            "data": []map[string]any{
                {"embedding": []float32{0.4, 0.5, 0.6}},
            },
        })
    }))
    defer srv.Close()

    client := llmgateway.NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
    resp, err := client.CreateEmbeddings(context.Background(), &llmgateway.EmbeddingRequest{
        Model: "embedding-3",
        Input: []string{"hello"},
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 3 {
        t.Errorf("unexpected embeddings: %v", resp.Embeddings)
    }
}

func TestZhipuClient_ErrorStatus(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusForbidden)
        w.Write([]byte(`{"error":"forbidden"}`)) //nolint:errcheck
    }))
    defer srv.Close()

    client := llmgateway.NewZhipuClientWithBase("bad-key", srv.URL, zap.NewNop())
    _, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
        Model:    "glm-4-flash",
        Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
    })
    if err == nil {
        t.Error("expected error, got nil")
    }
}
```

- [ ] **Step 3: 给 QwenClient 和 ZhipuClient 添加 WithBase 构造函数（测试需要）**

在 `qwen.go` 中追加：

```go
// NewQwenClientWithBase creates a QwenClient with a custom base URL (for testing).
func NewQwenClientWithBase(apiKey, baseURL string, logger *zap.Logger) *QwenClient {
    return &QwenClient{
        apiKey: apiKey,
        base:   baseURL,
        http:   &http.Client{Timeout: 60 * time.Second},
        logger: logger,
    }
}
```

在 `zhipu.go` 中追加：

```go
// NewZhipuClientWithBase creates a ZhipuClient with a custom base URL (for testing).
func NewZhipuClientWithBase(apiKey, baseURL string, logger *zap.Logger) *ZhipuClient {
    return &ZhipuClient{
        apiKey: apiKey,
        base:   baseURL,
        http:   &http.Client{Timeout: 60 * time.Second},
        logger: logger,
    }
}
```

- [ ] **Step 4: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v ./internal/llmgateway/...
```

Expected: 所有测试通过。

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/qwen.go internal/llmgateway/zhipu.go \
        internal/llmgateway/qwen_test.go internal/llmgateway/zhipu_test.go
git commit -m "test(llmgateway): add Qwen and Zhipu client unit tests"
```

---

## Task 6: DB Schema — 追加 rag_workspaces 到 tenant_schema.sql

**Files:**
- Modify: `pkg/tenantdb/tenant_schema.sql`

- [ ] **Step 1: 追加 rag_workspaces 表**

在文件末尾追加：

```sql

CREATE TABLE IF NOT EXISTS rag_workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: 验证 SQL 文件幂等性（本地 psql 或 diff 确认无其他变更）**

```bash
grep -n "rag_workspaces" /home/yang/go-projects/ClawHermes-AI-Go/pkg/tenantdb/tenant_schema.sql
```

Expected: 找到新建的表定义。

- [ ] **Step 3: Commit**

```bash
git add pkg/tenantdb/tenant_schema.sql
git commit -m "feat(db): add rag_workspaces table to tenant schema"
```

---

## Task 7: RAGHandler — 加 db 依赖 + workspace CRUD

**Files:**
- Modify: `api/handler/rag_handler.go`

- [ ] **Step 1: 更新结构体和构造函数**

将：

```go
type RAGHandler struct {
    ingestSvc  *knowledge.KnowledgeIngest
    ragService *knowledge.RAGService
    logger     *zap.Logger
}

func NewRAGHandler(
    ingestSvc *knowledge.KnowledgeIngest,
    ragService *knowledge.RAGService,
    logger *zap.Logger,
) *RAGHandler {
    return &RAGHandler{
        ingestSvc:  ingestSvc,
        ragService: ragService,
        logger:     logger,
    }
}
```

替换为：

```go
type RAGHandler struct {
    ingestSvc  *knowledge.KnowledgeIngest
    ragService *knowledge.RAGService
    db         *pgxpool.Pool
    logger     *zap.Logger
}

func NewRAGHandler(
    ingestSvc *knowledge.KnowledgeIngest,
    ragService *knowledge.RAGService,
    db *pgxpool.Pool,
    logger *zap.Logger,
) *RAGHandler {
    return &RAGHandler{
        ingestSvc:  ingestSvc,
        ragService: ragService,
        db:         db,
        logger:     logger,
    }
}
```

- [ ] **Step 2: 添加 WorkspaceConfig 类型和请求类型**

在 import 块后（在现有 request struct 定义之前）添加：

```go
type WorkspaceConfig struct {
    EmbeddingModel string `json:"embedding_model"`
    ChunkSize      int    `json:"chunk_size"`
    ChunkOverlap   int    `json:"chunk_overlap"`
    QueryMode      string `json:"query_mode"`
    TopK           int    `json:"top_k"`
}

var allowedEmbeddingModels = map[string]bool{
    "text-embedding-v3": true,
    "embedding-3":       true,
}

var allowedQueryModes = map[string]bool{
    "vector": true,
    "graph":  true,
    "hybrid": true,
}

type CreateWorkspaceRequest struct {
    Name        string          `json:"name" binding:"required"`
    Description string          `json:"description"`
    Config      WorkspaceConfig `json:"config"`
}

type UpdateWorkspaceRequest struct {
    Description *string          `json:"description"`
    Config      *WorkspaceConfig `json:"config"`
}
```

注意：这会替换现有的 `CreateWorkspaceRequest`（原来只有 `Workspace` 字段）。

- [ ] **Step 3: 重写 CreateWorkspace 方法**

将整个 `CreateWorkspace` 函数替换为：

```go
func (h *RAGHandler) CreateWorkspace(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req CreateWorkspaceRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    cfg := req.Config
    if cfg.EmbeddingModel == "" {
        cfg.EmbeddingModel = "text-embedding-v3"
    }
    if !allowedEmbeddingModels[cfg.EmbeddingModel] {
        c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported embedding model"})
        return
    }
    if cfg.QueryMode == "" {
        cfg.QueryMode = "hybrid"
    }
    if !allowedQueryModes[cfg.QueryMode] {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query_mode"})
        return
    }
    if cfg.ChunkSize <= 0 {
        cfg.ChunkSize = 512
    }
    if cfg.ChunkOverlap <= 0 {
        cfg.ChunkOverlap = 64
    }
    if cfg.TopK <= 0 {
        cfg.TopK = 5
    }

    schema := "tenant_" + tenantID
    var id string
    err := h.db.QueryRow(c.Request.Context(),
        fmt.Sprintf(`INSERT INTO "%s".rag_workspaces (name, description, config)
                     VALUES ($1, $2, $3) RETURNING id`, schema),
        req.Name, req.Description, cfg,
    ).Scan(&id)
    if err != nil {
        if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
            c.JSON(http.StatusConflict, gin.H{"error": "workspace already exists"})
            return
        }
        h.logger.Error("failed to create workspace", zap.String("tenant_id", tenantID), zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    h.logger.Info("workspace created", zap.String("name", req.Name), zap.String("tenant_id", tenantID))
    c.JSON(http.StatusCreated, gin.H{
        "id":          id,
        "name":        req.Name,
        "description": req.Description,
        "config":      cfg,
    })
}
```

- [ ] **Step 4: 重写 ListWorkspaces 方法**

将整个 `ListWorkspaces` 函数替换为：

```go
func (h *RAGHandler) ListWorkspaces(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }

    schema := "tenant_" + tenantID
    rows, err := h.db.Query(c.Request.Context(),
        fmt.Sprintf(`SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM "%s".rag_workspaces ORDER BY created_at DESC`, schema),
    )
    if err != nil {
        h.logger.Error("failed to list workspaces", zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    defer rows.Close()

    type row struct {
        ID          string          `json:"id"`
        Name        string          `json:"name"`
        Description string          `json:"description"`
        Config      WorkspaceConfig `json:"config"`
        CreatedAt   time.Time       `json:"created_at"`
        UpdatedAt   time.Time       `json:"updated_at"`
    }
    var workspaces []row
    for rows.Next() {
        var r row
        if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Config, &r.CreatedAt, &r.UpdatedAt); err != nil {
            h.logger.Error("failed to scan workspace row", zap.Error(err))
            c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
            return
        }
        workspaces = append(workspaces, r)
    }
    if workspaces == nil {
        workspaces = []row{}
    }
    c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}
```

- [ ] **Step 5: 添加 UpdateWorkspace 方法（重写现有占位实现）**

在文件中将 `DeleteWorkspace` 之前插入 `UpdateWorkspace`（如无则添加）：

```go
func (h *RAGHandler) UpdateWorkspace(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    name := c.Param("name")
    if name == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name required"})
        return
    }

    var req UpdateWorkspaceRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    schema := "tenant_" + tenantID

    // Read current config to enforce immutability
    var currentCfg WorkspaceConfig
    err := h.db.QueryRow(c.Request.Context(),
        fmt.Sprintf(`SELECT config FROM "%s".rag_workspaces WHERE name = $1`, schema),
        name,
    ).Scan(&currentCfg)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
        return
    }

    if req.Config != nil {
        if req.Config.EmbeddingModel != "" && req.Config.EmbeddingModel != currentCfg.EmbeddingModel {
            c.JSON(http.StatusBadRequest, gin.H{"error": "embedding_model is immutable after creation"})
            return
        }
        req.Config.EmbeddingModel = currentCfg.EmbeddingModel

        if req.Config.QueryMode != "" && !allowedQueryModes[req.Config.QueryMode] {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query_mode"})
            return
        }
        if req.Config.QueryMode == "" {
            req.Config.QueryMode = currentCfg.QueryMode
        }
        if req.Config.ChunkSize <= 0 {
            req.Config.ChunkSize = currentCfg.ChunkSize
        }
        if req.Config.ChunkOverlap <= 0 {
            req.Config.ChunkOverlap = currentCfg.ChunkOverlap
        }
        if req.Config.TopK <= 0 {
            req.Config.TopK = currentCfg.TopK
        }
    }

    var newDesc *string
    if req.Description != nil {
        newDesc = req.Description
    }
    newCfg := currentCfg
    if req.Config != nil {
        newCfg = *req.Config
    }

    _, err = h.db.Exec(c.Request.Context(),
        fmt.Sprintf(`UPDATE "%s".rag_workspaces
                     SET description = COALESCE($1, description),
                         config = $2,
                         updated_at = NOW()
                     WHERE name = $3`, schema),
        newDesc, newCfg, name,
    )
    if err != nil {
        h.logger.Error("failed to update workspace", zap.String("name", name), zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, gin.H{"name": name, "config": newCfg})
}
```

- [ ] **Step 6: 重写 DeleteWorkspace 方法**

将整个 `DeleteWorkspace` 函数替换为：

```go
func (h *RAGHandler) DeleteWorkspace(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    name := c.Param("name")
    if name == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name required"})
        return
    }

    schema := "tenant_" + tenantID
    tag, err := h.db.Exec(c.Request.Context(),
        fmt.Sprintf(`DELETE FROM "%s".rag_workspaces WHERE name = $1`, schema),
        name,
    )
    if err != nil {
        h.logger.Error("failed to delete workspace from db", zap.String("name", name), zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    if tag.RowsAffected() == 0 {
        c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
        return
    }

    h.logger.Info("workspace deleted", zap.String("name", name), zap.String("tenant_id", tenantID))
    c.JSON(http.StatusOK, gin.H{"success": true, "workspace": name})
}
```

- [ ] **Step 7: 更新 UploadDocument，从 DB 读取 workspace config（以备后续使用 embedding_model）**

找到 `UploadDocument` 中构建 `ingestReq` 的代码块，在其前方添加注释说明 config 读取点（当前 ingest 接口尚未接受 model 参数，保留现有逻辑不改动）：

不需要修改，保持现有 UploadDocument 逻辑不变。

- [ ] **Step 8: 添加所需 import**

在 rag_handler.go 的 import 块中确认包含：

```go
import (
    "fmt"
    "mime/multipart"
    "net/http"
    "strings"
    "time"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.uber.org/zap"
)
```

- [ ] **Step 9: 编译**

```bash
go build ./api/handler/...
```

Expected: 成功（若有类型问题按编译错误修复）。

- [ ] **Step 10: Commit**

```bash
git add api/handler/rag_handler.go
git commit -m "feat(rag): add db persistence to RAGHandler with workspace CRUD"
```

---

## Task 8: Router — 注册 workspace 路由

**Files:**
- Modify: `api/router.go`

- [ ] **Step 1: 更新 NewRAGHandler 调用**

找到：
```go
ragHandler := handler.NewRAGHandler(ingestSvc, ragService, logger)
```

替换为：
```go
ragHandler := handler.NewRAGHandler(ingestSvc, ragService, db, logger)
```

- [ ] **Step 2: 替换 knowledge 路由组**

找到：
```go
// Knowledge endpoints
knowledge := router.Group("/knowledge")
{
    knowledge.POST("/ingest", ragHandler.UploadDocument)
    knowledge.POST("/query", ragHandler.Query)
}
```

替换为：
```go
// Knowledge endpoints — 所有路由均需 JWT + 租户上下文
var knowledgeMW []gin.HandlerFunc
if jwtSvc != nil {
    knowledgeMW = append(knowledgeMW, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
}
knowledgeGroup := router.Group("/knowledge", knowledgeMW...)
{
    // member 可访问
    knowledgeGroup.GET("/workspaces", ragHandler.ListWorkspaces)
    knowledgeGroup.GET("/workspaces/:name/stats", ragHandler.GetWorkspaceStats)
    knowledgeGroup.POST("/query", ragHandler.Query)

    // admin/owner 专属
    var adminMW []gin.HandlerFunc
    if jwtSvc != nil {
        adminMW = append(adminMW, middleware.RequireTenantRole("admin"))
    }
    knowledgeGroup.POST("/workspaces", append(adminMW, ragHandler.CreateWorkspace)...)
    knowledgeGroup.PATCH("/workspaces/:name", append(adminMW, ragHandler.UpdateWorkspace)...)
    knowledgeGroup.DELETE("/workspaces/:name", append(adminMW, ragHandler.DeleteWorkspace)...)
    knowledgeGroup.POST("/ingest", append(adminMW, ragHandler.UploadDocument)...)
}
```

- [ ] **Step 3: 编译**

```bash
go build ./api/...
```

- [ ] **Step 4: 运行全套测试**

```bash
go test -short ./...
```

Expected: 所有测试通过（rag_handler_test.go 可能失败，Task 9 修复）。

- [ ] **Step 5: Commit**

```bash
git add api/router.go
git commit -m "feat(api): register workspace CRUD routes with JWT and tenant middleware"
```

---

## Task 9: 后端测试 — rag_handler_test.go workspace CRUD

**Files:**
- Modify: `api/handler/rag_handler_test.go`（如不存在则创建）

- [ ] **Step 1: 检查是否存在 rag_handler_test.go**

```bash
ls /home/yang/go-projects/ClawHermes-AI-Go/api/handler/rag_handler_test.go
```

- [ ] **Step 2: 确认 tenantIDFromCtx helper 位置**

```bash
grep -n "tenantIDFromCtx\|respondMissingTenant" \
  /home/yang/go-projects/ClawHermes-AI-Go/api/handler/*.go | head -20
```

- [ ] **Step 3: 编写 / 更新 rag_handler_test.go**

```go
package handler_test

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
    "github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
    "github.com/gin-gonic/gin"
    "go.uber.org/zap"
)

type mockIngestSvc struct{}
type mockRAGSvc struct{}

func (m *mockIngestSvc) IngestDocument(_ context.Context, req knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
    return &knowledge.IngestResult{DocumentID: req.DocumentID, Workspace: req.Workspace}, nil
}
func (m *mockIngestSvc) GetWorkspaceStats(_ context.Context, _ string) (map[string]any, error) {
    return map[string]any{"vectors": 0}, nil
}
func (m *mockRAGSvc) Query(_ context.Context, _ knowledge.RAGQueryRequest) (*knowledge.RAGResult, error) {
    return &knowledge.RAGResult{Answer: "ok"}, nil
}
func (m *mockRAGSvc) GetWorkspaceCollections(_ context.Context) ([]string, error) {
    return nil, nil
}

// setTenantCtx injects a fake tenant_id into the gin context for tests.
func setTenantCtx(tenantID string) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Set(middleware.TenantIDKey, tenantID)
        c.Next()
    }
}

func newTestRAGHandler(t *testing.T) *handler.RAGHandler {
    t.Helper()
    // db = nil; workspace CRUD tests that need DB should use pgxmock or skip
    return handler.NewRAGHandler(&mockIngestSvc{}, &mockRAGSvc{}, nil, zap.NewNop())
}

func TestListWorkspaces_MissingTenant(t *testing.T) {
    gin.SetMode(gin.TestMode)
    r := gin.New()
    h := newTestRAGHandler(t)
    r.GET("/knowledge/workspaces", h.ListWorkspaces)

    w := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/knowledge/workspaces", nil)
    r.ServeHTTP(w, req)

    if w.Code != http.StatusUnauthorized {
        t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
    }
}

func TestCreateWorkspace_MissingTenant(t *testing.T) {
    gin.SetMode(gin.TestMode)
    r := gin.New()
    h := newTestRAGHandler(t)
    r.POST("/knowledge/workspaces", h.CreateWorkspace)

    body, _ := json.Marshal(map[string]any{"name": "test"})
    w := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)

    if w.Code != http.StatusUnauthorized {
        t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
    }
}

func TestCreateWorkspace_InvalidEmbeddingModel(t *testing.T) {
    gin.SetMode(gin.TestMode)
    r := gin.New()
    h := newTestRAGHandler(t)
    r.POST("/knowledge/workspaces", setTenantCtx("test-tenant"), h.CreateWorkspace)

    body, _ := json.Marshal(map[string]any{
        "name": "test",
        "config": map[string]any{"embedding_model": "gpt-4"},
    })
    w := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)

    if w.Code != http.StatusBadRequest {
        t.Errorf("want 400, got %d", w.Code)
    }
    var resp map[string]string
    json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
    if resp["error"] != "unsupported embedding model" {
        t.Errorf("unexpected error: %q", resp["error"])
    }
}

func TestQuery_MissingTenant(t *testing.T) {
    gin.SetMode(gin.TestMode)
    r := gin.New()
    h := newTestRAGHandler(t)
    r.POST("/knowledge/query", h.Query)

    body, _ := json.Marshal(map[string]any{
        "question": "hello", "workspace": "ws", "mode": "hybrid",
    })
    w := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/knowledge/query", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)

    if w.Code != http.StatusUnauthorized {
        t.Errorf("want 401, got %d", w.Code)
    }
}
```

注意：`handler.RAGHandler` 和 mock service 需要接口化才能直接注入 mock。若现有代码用具体类型（`*knowledge.KnowledgeIngest`），则跳过 mock 注入部分，仅测试 missing-tenant 和 validation 路径（不需要 DB/service）。

- [ ] **Step 4: 运行测试**

```bash
go test -v ./api/handler/... -run TestListWorkspaces -run TestCreateWorkspace -run TestQuery
```

Expected: 通过（或按编译错误调整 mock 方法签名）。

- [ ] **Step 5: Commit**

```bash
git add api/handler/rag_handler_test.go
git commit -m "test(rag): add workspace CRUD handler unit tests"
```

---

## Task 10: Frontend — api.js 新增知识库 API 函数

**Files:**
- Modify: `web/src/services/api.js`

- [ ] **Step 1: 在文件末尾（`export default api` 之前）追加**

```js
// Knowledge Workspaces
export const listWorkspaces = () => api.get('/knowledge/workspaces');
export const createWorkspace = (data) => api.post('/knowledge/workspaces', data);
export const getWorkspaceStats = (name) => api.get(`/knowledge/workspaces/${name}/stats`);
export const updateWorkspace = (name, data) => api.patch(`/knowledge/workspaces/${name}`, data);
export const deleteWorkspace = (name) => api.delete(`/knowledge/workspaces/${name}`);

// Knowledge Ingest & Query
export const ingestDocument = (formData) =>
  api.post('/knowledge/ingest', formData, { headers: { 'Content-Type': 'multipart/form-data' } });
export const queryKnowledge = (data) => api.post('/knowledge/query', data);
```

- [ ] **Step 2: 验证无语法错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web && npm run lint 2>&1 | head -30
```

Expected: 0 errors (新增行无 lint 问题)。

- [ ] **Step 3: Commit**

```bash
git add web/src/services/api.js
git commit -m "feat(frontend): add knowledge workspace API functions"
```

---

## Task 11: Frontend — KnowledgePage.jsx（知识库列表）

**Files:**
- Create: `web/src/pages/KnowledgePage.jsx`

- [ ] **Step 1: 创建 KnowledgePage.jsx**

```jsx
import React, { useState, useEffect, useCallback } from 'react';
import {
  Table, Button, Modal, Form, Input, Select, InputNumber,
  Space, Popconfirm, message, Tag,
} from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { listWorkspaces, createWorkspace, deleteWorkspace } from '../services/api';
import { useAuth } from '../hooks/useAuth';

const { Option } = Select;

const KnowledgePage = () => {
  const [workspaces, setWorkspaces] = useState([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const { user } = useAuth();

  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const fetchWorkspaces = useCallback(async () => {
    setLoading(true);
    try {
      const res = await listWorkspaces();
      setWorkspaces(res.data.workspaces || []);
    } catch (err) {
      message.error(err.response?.data?.error || '加载知识库失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    fetchWorkspaces().then(() => { if (cancelled) return; });
    return () => { cancelled = true; };
  }, [fetchWorkspaces]);

  const handleCreate = async (values) => {
    setSubmitting(true);
    try {
      await createWorkspace({
        name: values.name,
        description: values.description || '',
        config: {
          embedding_model: values.embedding_model,
          chunk_size: values.chunk_size,
          chunk_overlap: values.chunk_overlap,
          query_mode: values.query_mode,
          top_k: values.top_k,
        },
      });
      message.success('知识库创建成功');
      setModalOpen(false);
      form.resetFields();
      fetchWorkspaces();
    } catch (err) {
      message.error(err.response?.data?.error || '创建失败');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (name) => {
    try {
      await deleteWorkspace(name);
      message.success('已删除');
      fetchWorkspaces();
    } catch (err) {
      message.error(err.response?.data?.error || '删除失败');
    }
  };

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      render: (name) => (
        <Button type="link" onClick={() => navigate(`/knowledge/${name}`)}>{name}</Button>
      ),
    },
    { title: '描述', dataIndex: 'description', key: 'description' },
    {
      title: 'Embedding 模型',
      dataIndex: ['config', 'embedding_model'],
      key: 'embedding_model',
      render: (model) => <Tag>{model || '-'}</Tag>,
    },
    {
      title: '查询模式',
      dataIndex: ['config', 'query_mode'],
      key: 'query_mode',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (t) => new Date(t).toLocaleString('zh-CN'),
    },
    ...(isAdmin ? [{
      title: '操作',
      key: 'action',
      render: (_, record) => (
        <Popconfirm
          title={`确认删除知识库「${record.name}」？此操作不可逆。`}
          onConfirm={() => handleDelete(record.name)}
          okText="删除"
          cancelText="取消"
          okButtonProps={{ danger: true }}
        >
          <Button danger size="small">删除</Button>
        </Popconfirm>
      ),
    }] : []),
  ];

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Space style={{ justifyContent: 'space-between', width: '100%' }}>
        <span style={{ fontSize: 16, fontWeight: 500 }}>知识库管理</span>
        {isAdmin && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
            新建知识库
          </Button>
        )}
      </Space>

      <Table
        dataSource={workspaces}
        columns={columns}
        rowKey="id"
        loading={loading}
        pagination={{ pageSize: 10 }}
      />

      <Modal
        title="新建知识库"
        open={modalOpen}
        onCancel={() => { setModalOpen(false); form.resetFields(); }}
        footer={null}
        destroyOnClose
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{
            embedding_model: 'text-embedding-v3',
            chunk_size: 512,
            chunk_overlap: 64,
            query_mode: 'hybrid',
            top_k: 5,
          }}
          onFinish={handleCreate}
        >
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入知识库名称' }]}>
            <Input placeholder="英文字母、数字、下划线" maxLength={64} />
          </Form.Item>
          <Form.Item label="描述" name="description">
            <Input.TextArea rows={2} maxLength={256} />
          </Form.Item>
          <Form.Item label="Embedding 模型" name="embedding_model" rules={[{ required: true }]}>
            <Select>
              <Option value="text-embedding-v3">text-embedding-v3（Qwen，1536 维）</Option>
              <Option value="embedding-3">embedding-3（智谱，2048 维）</Option>
            </Select>
          </Form.Item>
          <Form.Item label="分块大小" name="chunk_size">
            <InputNumber min={128} max={4096} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label="分块重叠" name="chunk_overlap">
            <InputNumber min={0} max={512} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label="查询模式" name="query_mode">
            <Select>
              <Option value="vector">vector（纯向量检索）</Option>
              <Option value="graph">graph（纯图检索）</Option>
              <Option value="hybrid">hybrid（混合检索）</Option>
            </Select>
          </Form.Item>
          <Form.Item label="Top K" name="top_k">
            <InputNumber min={1} max={20} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={submitting}>创建</Button>
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  );
};

export default KnowledgePage;
```

- [ ] **Step 2: Lint 检查**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web && npm run lint 2>&1 | tail -20
```

Expected: 0 errors。

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/KnowledgePage.jsx
git commit -m "feat(frontend): add KnowledgePage workspace list with create/delete"
```

---

## Task 12: Frontend — KnowledgeDetailPage.jsx（知识库详情）

**Files:**
- Create: `web/src/pages/KnowledgeDetailPage.jsx`

- [ ] **Step 1: 创建 KnowledgeDetailPage.jsx**

```jsx
import React, { useState, useEffect, useCallback } from 'react';
import {
  Card, Descriptions, Form, Input, Select, InputNumber, Button,
  Upload, Space, Divider, message, Spin, Tag,
} from 'antd';
import { UploadOutlined, ArrowLeftOutlined } from '@ant-design/icons';
import { useParams, useNavigate } from 'react-router-dom';
import {
  getWorkspaceStats, updateWorkspace, ingestDocument, queryKnowledge,
} from '../services/api';
import { useAuth } from '../hooks/useAuth';

const { Option } = Select;
const { TextArea } = Input;

const KnowledgeDetailPage = () => {
  const { name } = useParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const [stats, setStats] = useState(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [configForm] = Form.useForm();
  const [configSaving, setConfigSaving] = useState(false);
  const [uploadList, setUploadList] = useState([]);
  const [uploading, setUploading] = useState(false);
  const [queryForm] = Form.useForm();
  const [queryResult, setQueryResult] = useState(null);
  const [querying, setQuerying] = useState(false);

  const fetchStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const res = await getWorkspaceStats(name);
      setStats(res.data.stats);
      const cfg = res.data.config || {};
      configForm.setFieldsValue({
        description: res.data.description || '',
        chunk_size: cfg.chunk_size || 512,
        chunk_overlap: cfg.chunk_overlap || 64,
        query_mode: cfg.query_mode || 'hybrid',
        top_k: cfg.top_k || 5,
      });
    } catch (err) {
      message.error(err.response?.data?.error || '加载详情失败');
    } finally {
      setStatsLoading(false);
    }
  }, [name, configForm]);

  useEffect(() => {
    let cancelled = false;
    fetchStats().then(() => { if (cancelled) return; });
    return () => { cancelled = true; };
  }, [fetchStats]);

  const handleConfigSave = async (values) => {
    setConfigSaving(true);
    try {
      await updateWorkspace(name, {
        description: values.description,
        config: {
          chunk_size: values.chunk_size,
          chunk_overlap: values.chunk_overlap,
          query_mode: values.query_mode,
          top_k: values.top_k,
        },
      });
      message.success('配置已保存');
      fetchStats();
    } catch (err) {
      message.error(err.response?.data?.error || '保存失败');
    } finally {
      setConfigSaving(false);
    }
  };

  const handleUpload = async () => {
    if (uploadList.length === 0) {
      message.warning('请先选择文件');
      return;
    }
    setUploading(true);
    try {
      for (const file of uploadList) {
        const fd = new FormData();
        fd.append('workspace', name);
        fd.append('file', file);
        await ingestDocument(fd);
      }
      message.success(`已上传 ${uploadList.length} 个文件`);
      setUploadList([]);
      fetchStats();
    } catch (err) {
      message.error(err.response?.data?.error || '上传失败');
    } finally {
      setUploading(false);
    }
  };

  const handleQuery = async (values) => {
    setQuerying(true);
    setQueryResult(null);
    try {
      const res = await queryKnowledge({
        question: values.question,
        workspace: name,
        mode: values.mode,
        topK: values.topK || 5,
      });
      setQueryResult(res.data);
    } catch (err) {
      message.error(err.response?.data?.error || '查询失败');
    } finally {
      setQuerying(false);
    }
  };

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Space>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/knowledge')}>返回</Button>
        <span style={{ fontSize: 16, fontWeight: 500 }}>知识库：{name}</span>
      </Space>

      <Card title="统计信息" loading={statsLoading} size="small">
        {stats && (
          <Descriptions size="small" column={3}>
            {Object.entries(stats).map(([k, v]) => (
              <Descriptions.Item key={k} label={k}>{String(v)}</Descriptions.Item>
            ))}
          </Descriptions>
        )}
      </Card>

      {isAdmin && (
        <Card title="配置" size="small">
          <Form form={configForm} layout="vertical" onFinish={handleConfigSave}>
            <Form.Item label="描述" name="description">
              <Input.TextArea rows={2} maxLength={256} />
            </Form.Item>
            <Form.Item label="分块大小" name="chunk_size">
              <InputNumber min={128} max={4096} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item label="分块重叠" name="chunk_overlap">
              <InputNumber min={0} max={512} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item label="查询模式" name="query_mode">
              <Select>
                <Option value="vector">vector</Option>
                <Option value="graph">graph</Option>
                <Option value="hybrid">hybrid</Option>
              </Select>
            </Form.Item>
            <Form.Item label="Top K" name="top_k">
              <InputNumber min={1} max={20} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={configSaving}>保存配置</Button>
            </Form.Item>
          </Form>
        </Card>
      )}

      {isAdmin && (
        <Card title="上传文档" size="small">
          <Space direction="vertical" style={{ width: '100%' }}>
            <Upload
              multiple
              beforeUpload={(file) => {
                setUploadList((prev) => [...prev, file]);
                return false;
              }}
              onRemove={(file) => {
                setUploadList((prev) => prev.filter((f) => f.uid !== file.uid));
              }}
              fileList={uploadList.map((f) => ({ uid: f.uid, name: f.name, status: 'done' }))}
            >
              <Button icon={<UploadOutlined />}>选择文件</Button>
            </Upload>
            <Button
              type="primary"
              onClick={handleUpload}
              loading={uploading}
              disabled={uploadList.length === 0}
            >
              开始上传（{uploadList.length} 个文件）
            </Button>
          </Space>
        </Card>
      )}

      <Card title="查询测试" size="small">
        <Form
          form={queryForm}
          layout="vertical"
          initialValues={{ mode: 'hybrid', topK: 5 }}
          onFinish={handleQuery}
        >
          <Form.Item label="问题" name="question" rules={[{ required: true, message: '请输入问题' }]}>
            <TextArea rows={2} placeholder="输入问题..." />
          </Form.Item>
          <Space>
            <Form.Item label="模式" name="mode" style={{ marginBottom: 0 }}>
              <Select style={{ width: 120 }}>
                <Option value="vector">vector</Option>
                <Option value="graph">graph</Option>
                <Option value="hybrid">hybrid</Option>
              </Select>
            </Form.Item>
            <Form.Item label="Top K" name="topK" style={{ marginBottom: 0 }}>
              <InputNumber min={1} max={20} />
            </Form.Item>
          </Space>
          <Form.Item style={{ marginTop: 12 }}>
            <Button type="primary" htmlType="submit" loading={querying}>查询</Button>
          </Form.Item>
        </Form>

        {queryResult && (
          <div style={{ marginTop: 16 }}>
            <Divider />
            <div style={{ marginBottom: 12 }}>
              <strong>回答：</strong>
              <div style={{ marginTop: 8, whiteSpace: 'pre-wrap' }}>{queryResult.answer}</div>
            </div>
            {queryResult.sources?.length > 0 && (
              <div>
                <strong>来源文档：</strong>
                {queryResult.sources.map((s, i) => (
                  <Card key={i} size="small" style={{ marginTop: 8 }}>
                    <Tag>文档 {s.document_id?.slice(0, 8)}</Tag>
                    <Tag>chunk {s.chunk_index}</Tag>
                    <Tag color="blue">score: {s.score?.toFixed(3)}</Tag>
                    <div style={{ marginTop: 8, color: '#666', fontSize: 12 }}>{s.content}</div>
                  </Card>
                ))}
              </div>
            )}
          </div>
        )}
      </Card>
    </Space>
  );
};

export default KnowledgeDetailPage;
```

- [ ] **Step 2: Lint 检查**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web && npm run lint 2>&1 | tail -20
```

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/KnowledgeDetailPage.jsx
git commit -m "feat(frontend): add KnowledgeDetailPage with config/upload/query"
```

---

## Task 13: Frontend — App.jsx 注册路由 + 侧边栏菜单

**Files:**
- Modify: `web/src/App.jsx`

- [ ] **Step 1: 添加 import**

在现有 import 块中添加：

```jsx
import KnowledgePage from './pages/KnowledgePage';
import KnowledgeDetailPage from './pages/KnowledgeDetailPage';
```

以及 icon：
```jsx
import { ..., BookOutlined } from '@ant-design/icons';
```

（`BookOutlined` 追加到现有解构 import 中）

- [ ] **Step 2: 在 menuItems 数组中追加菜单项**

在 `{ key: '/memory', ... }` 一行之后追加：

```jsx
{ key: '/knowledge', icon: <BookOutlined />, label: <Link to="/knowledge">知识库</Link> },
```

- [ ] **Step 3: 在 Routes 中注册两条路由**

在 `<Route path="/memory" .../>` 之后追加：

```jsx
<Route path="/knowledge" element={<PrivateRoute><KnowledgePage /></PrivateRoute>} />
<Route path="/knowledge/:name" element={<PrivateRoute><KnowledgeDetailPage /></PrivateRoute>} />
```

- [ ] **Step 4: Lint + Build**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run lint 2>&1 | tail -10
npm run build 2>&1 | tail -20
```

Expected: lint 0 errors，build 成功。

- [ ] **Step 5: Commit**

```bash
git add web/src/App.jsx
git commit -m "feat(frontend): register /knowledge and /knowledge/:name routes in App"
```

---

## Task 14: GetWorkspaceStats — 修复 handler 使用 DB

**Files:**
- Modify: `api/handler/rag_handler.go`

GetWorkspaceStats 目前从 `ingestSvc` 获取 stats（Milvus），需要先从 PG 读 config 确认 workspace 存在，再返回 stats。

- [ ] **Step 1: 重写 GetWorkspaceStats**

将现有 `GetWorkspaceStats` 函数替换为：

```go
func (h *RAGHandler) GetWorkspaceStats(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    name := c.Param("name")
    if name == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name required"})
        return
    }

    schema := "tenant_" + tenantID
    var cfg WorkspaceConfig
    var description string
    err := h.db.QueryRow(c.Request.Context(),
        fmt.Sprintf(`SELECT COALESCE(description,''), config
                     FROM "%s".rag_workspaces WHERE name = $1`, schema),
        name,
    ).Scan(&description, &cfg)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
        return
    }

    milvusStats, err := h.ingestSvc.GetWorkspaceStats(c.Request.Context(), name)
    if err != nil {
        h.logger.Warn("failed to get milvus stats", zap.String("workspace", name), zap.Error(err))
        milvusStats = map[string]any{"error": err.Error()}
    }

    c.JSON(http.StatusOK, gin.H{
        "name":        name,
        "description": description,
        "config":      cfg,
        "stats":       milvusStats,
    })
}
```

- [ ] **Step 2: 编译**

```bash
go build ./api/handler/...
```

- [ ] **Step 3: 运行全套短测试**

```bash
go test -short ./...
```

- [ ] **Step 4: Commit**

```bash
git add api/handler/rag_handler.go
git commit -m "fix(rag): GetWorkspaceStats reads config from PG before querying Milvus"
```

---

## Task 15: 集成验证 checklist

手工验证以下路径（需要运行后端 + 前端 + PG）：

- [ ] `GET /health` → 200
- [ ] 登录 → 侧边栏出现「知识库」菜单
- [ ] 访问 `/knowledge` → 空列表（新租户）
- [ ] admin 用户点「新建知识库」→ 填写 name/config → 提交 → 列表刷新
- [ ] 普通 member 无「新建」和「删除」按钮
- [ ] 点击知识库名称 → 进入详情页，显示 stats
- [ ] admin 修改配置 → 保存 → 刷新后保持
- [ ] admin 尝试修改 embedding_model → 后端返回 400
- [ ] admin 上传文档（PDF 或 txt）
- [ ] 查询测试 → 返回回答和来源
- [ ] 删除知识库 → 从列表消失

---

## Task 16: 最终 Build 验证

- [ ] **后端全套测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go vet ./...
go test -short ./...
```

Expected: 无错误，无失败。

- [ ] **前端 lint + build**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run lint
npm run build
```

Expected: lint 0 warnings，build 成功。

- [ ] **最终 Commit（若有未提交变更）**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git status
# 按需 add + commit
```

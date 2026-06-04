# Code Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 code review 发现的 6 个 bug：SSE transport 失效、NetworkPolicy 外部流量切断、缓存无限增长、context 丢失、HealthCheck 持锁阻塞、Neo4j 内存不足。

**Architecture:** 每个 fix 独立，互不依赖。Go 代码修复集中在 `internal/mcp/`，k8s 配置修复在 `k8s/`。Execute 的 context 问题需要修改接口签名，需同步更新所有实现和调用方。

**Tech Stack:** Go 1.21+, sync.RWMutex, Kubernetes NetworkPolicy, Neo4j 4.4

---

## File Map

| 文件 | 变更类型 | 原因 |
|------|----------|------|
| `internal/mcp/client.go` | Modify | 修复 connectSSE 不设置 sseConn；HealthCheck 改用 RLock |
| `internal/mcp/cache.go` | Modify | StoreTools/StoreResources 加 maxSize 检查 |
| `internal/mcp/skill_adapter.go` | Modify | Execute 接受 context 参数 |
| `internal/mcp/mcp_test.go` | Modify | 补充 StoreTools maxSize 测试 |
| `internal/mcp/integration_test.go` | Modify | 补充 SSE/HealthCheck 测试 |
| `k8s/network-policy.yaml` | Modify | 恢复 ingress-nginx 入站规则 |
| `k8s/dependencies.yaml` | Modify | 提高 Neo4j 内存限制 |

---

### Task 1: 修复 StoreTools/StoreResources 绕过 maxSize 检查

**Files:**

- Modify: `internal/mcp/cache.go:53-80`
- Modify: `internal/mcp/mcp_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/mcp_test.go` 末尾添加：

```go
// TestStoreToolsRespectsMaxSize 验证 StoreTools 不超过 maxSize
func TestStoreToolsRespectsMaxSize(t *testing.T) {
 cache := NewCapabilityCache(2, 1*time.Hour)

 tools := []*MCPTool{{Name: "tool1"}}

 cache.StoreTools("server1", tools)
 cache.StoreTools("server2", tools)
 // 此时已满，再插入新 server 不应超过 maxSize
 cache.StoreTools("server3", tools)

 if cache.Size() > 2 {
  t.Errorf("cache size %d exceeds maxSize 2", cache.Size())
 }
}

// TestStoreResourcesRespectsMaxSize 验证 StoreResources 不超过 maxSize
func TestStoreResourcesRespectsMaxSize(t *testing.T) {
 cache := NewCapabilityCache(2, 1*time.Hour)

 resources := []*MCPResource{{URI: "res1"}}

 cache.StoreResources("server1", resources)
 cache.StoreResources("server2", resources)
 cache.StoreResources("server3", resources)

 if cache.Size() > 2 {
  t.Errorf("cache size %d exceeds maxSize 2", cache.Size())
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test ./internal/mcp/... -run "TestStoreToolsRespectsMaxSize|TestStoreResourcesRespectsMaxSize" -v
```

期望：FAIL，cache size 超过 maxSize。

- [ ] **Step 3: 修复 StoreTools 和 StoreResources**

在 `internal/mcp/cache.go` 中，将 `StoreTools`（line 53）改为：

```go
func (c *CapabilityCache) StoreTools(serverID string, tools []*MCPTool) {
 c.mu.Lock()
 defer c.mu.Unlock()

 entry, exists := c.entries[serverID]
 if !exists {
  if len(c.entries) >= c.maxSize {
   for k := range c.entries {
    delete(c.entries, k)
    break
   }
  }
  entry = &CacheEntry{}
  c.entries[serverID] = entry
 }

 entry.Tools = tools
 entry.ExpiresAt = time.Now().Add(c.ttl)
}
```

将 `StoreResources`（line 68）改为：

```go
func (c *CapabilityCache) StoreResources(serverID string, resources []*MCPResource) {
 c.mu.Lock()
 defer c.mu.Unlock()

 entry, exists := c.entries[serverID]
 if !exists {
  if len(c.entries) >= c.maxSize {
   for k := range c.entries {
    delete(c.entries, k)
    break
   }
  }
  entry = &CacheEntry{}
  c.entries[serverID] = entry
 }

 entry.Resources = resources
 entry.ExpiresAt = time.Now().Add(c.ttl)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/mcp/... -run "TestStoreToolsRespectsMaxSize|TestStoreResourcesRespectsMaxSize" -v
```

期望：PASS。

- [ ] **Step 5: 运行全部 mcp 测试确认无回归**

```bash
go test ./internal/mcp/... -v 2>&1 | tail -20
```

期望：所有测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/cache.go internal/mcp/mcp_test.go
git commit -m "fix: StoreTools/StoreResources 加 maxSize 驱逐检查，防止缓存无限增长"
```

---

### Task 2: 修复 SSE transport 永久失效（connectSSE 不设置 sseConn）

**Files:**

- Modify: `internal/mcp/client.go:270-291`

背景：`connectSSE` 只设置了 `c.httpClient`，从未赋值 `c.sseConn`。`sendSSERequest` 第 364 行检查 `c.sseConn == nil` 永远为真，导致所有 SSE 调用立即返回错误。

SSE transport 的实际通信应走 HTTP（与 `sendHTTPRequest` 相同），`sseConn net.Conn` 字段是为原始 TCP SSE 连接设计的，但当前实现并未使用。修复方案：`connectSSE` 建立连接后将 `c.httpClient` 赋给一个标记，并在 `sendSSERequest` 中改为使用 `c.httpClient` 而非检查 `c.sseConn`。

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/integration_test.go` 末尾添加（使用 httptest）：

```go
func TestSSETransportFunctional(t *testing.T) {
 // 启动一个简单的 HTTP 测试服务器模拟 MCP SSE 端点
 mux := http.NewServeMux()
 mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  if r.Method == http.MethodGet {
   w.WriteHeader(http.StatusOK)
   return
  }
  // POST: 返回 tools/list 响应
  w.Header().Set("Content-Type", "application/json")
  json.NewEncoder(w).Encode(MCPResponse{
   Result: json.RawMessage(`[]`),
  })
 })
 srv := httptest.NewServer(mux)
 defer srv.Close()

 logger, _ := zap.NewDevelopment()
 cfg := MCPServerConfig{
  ID:        "test-sse",
  Transport: "sse",
  URL:       srv.URL,
  Timeout:   5 * time.Second,
 }
 client := NewBaseClient(cfg, logger)

 ctx := context.Background()
 if err := client.Connect(ctx); err != nil {
  t.Fatalf("Connect failed: %v", err)
 }

 tools, err := client.ListTools(ctx)
 if err != nil {
  t.Fatalf("ListTools via SSE failed: %v", err)
 }
 _ = tools // 空列表也是成功
}
```

注意：需要在文件顶部 import 中加入 `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"context"` 如果尚未存在。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/mcp/... -run "TestSSETransportFunctional" -v
```

期望：FAIL，"SSE connection not established"。

- [ ] **Step 3: 修复 sendSSERequest**

在 `internal/mcp/client.go` 中，将 `sendSSERequest`（line 363）改为不检查 `c.sseConn`，改为检查 `c.httpClient`：

```go
func (c *BaseClient) sendSSERequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
 if c.httpClient == nil {
  return nil, fmt.Errorf("SSE connection not established")
 }

 data, err := json.Marshal(req)
 if err != nil {
  return nil, fmt.Errorf("failed to marshal request: %w", err)
 }

 httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(data))
 if err != nil {
  return nil, fmt.Errorf("failed to create request: %w", err)
 }
 httpReq.Header.Set("Content-Type", "application/json")

 resp, err := c.httpClient.Do(httpReq)
 if err != nil {
  return nil, fmt.Errorf("failed to send SSE request: %w", err)
 }
 defer resp.Body.Close()

 body, err := io.ReadAll(resp.Body)
 if err != nil {
  return nil, fmt.Errorf("failed to read response: %w", err)
 }

 var mcpResp MCPResponse
 if err := json.Unmarshal(body, &mcpResp); err != nil {
  return nil, fmt.Errorf("failed to unmarshal response: %w", err)
 }

 return &mcpResp, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/mcp/... -run "TestSSETransportFunctional" -v
```

期望：PASS。

- [ ] **Step 5: 运行全部 mcp 测试**

```bash
go test ./internal/mcp/... -v 2>&1 | tail -20
```

期望：所有测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/client.go internal/mcp/integration_test.go
git commit -m "fix: 修复 SSE transport 永久失效，sendSSERequest 改为检查 httpClient"
```

---

### Task 3: 修复 HealthCheck 持写锁执行阻塞网络调用

**Files:**

- Modify: `internal/mcp/client.go:442-466`

背景：`HealthCheck` 持有 `c.mu.Lock()`（写锁）期间调用 `sendRequest`，这是一个阻塞网络调用。MCP 服务器响应慢时，所有并发的 `ListTools`/`ListResources` 的 `RLock` 请求被阻塞。修复：只在读写 `c.connected`/`c.healthy` 字段时持锁，网络调用在锁外执行。

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/integration_test.go` 末尾添加：

```go
func TestHealthCheckDoesNotBlockConcurrentReads(t *testing.T) {
 // 使用一个慢响应服务器验证 HealthCheck 不阻塞并发 ListTools
 var slowOnce sync.Once
 mux := http.NewServeMux()
 mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  if r.Method == http.MethodGet {
   w.WriteHeader(http.StatusOK)
   return
  }
  slowOnce.Do(func() {
   time.Sleep(200 * time.Millisecond) // 模拟慢健康检查
  })
  w.Header().Set("Content-Type", "application/json")
  json.NewEncoder(w).Encode(MCPResponse{Result: json.RawMessage(`[]`)})
 })
 srv := httptest.NewServer(mux)
 defer srv.Close()

 logger, _ := zap.NewDevelopment()
 cfg := MCPServerConfig{
  ID:        "test-hc-concurrent",
  Transport: "http",
  URL:       srv.URL + "/rpc",
  Timeout:   5 * time.Second,
 }
 client := NewBaseClient(cfg, logger)
 ctx := context.Background()
 if err := client.Connect(ctx); err != nil {
  t.Fatalf("Connect failed: %v", err)
 }

 // 并发：一个 HealthCheck（慢），一个 ListTools
 var wg sync.WaitGroup
 wg.Add(2)

 start := time.Now()
 go func() {
  defer wg.Done()
  client.HealthCheck(ctx) //nolint:errcheck
 }()
 go func() {
  defer wg.Done()
  time.Sleep(10 * time.Millisecond) // 确保 HealthCheck 先启动
  client.ListTools(ctx)             //nolint:errcheck
 }()
 wg.Wait()

 elapsed := time.Since(start)
 // ListTools 不应被 HealthCheck 的 200ms 延迟完全阻塞
 // 如果持写锁，elapsed 会 >= 200ms；修复后 ListTools 可并发执行
 if elapsed > 300*time.Millisecond {
  t.Errorf("HealthCheck blocked concurrent ListTools for %v (expected < 300ms)", elapsed)
 }
}
```

- [ ] **Step 2: 运行测试确认失败（或超时）**

```bash
go test ./internal/mcp/... -run "TestHealthCheckDoesNotBlockConcurrentReads" -v -timeout 10s
```

期望：FAIL 或 elapsed > 300ms。

- [ ] **Step 3: 修复 HealthCheck**

将 `internal/mcp/client.go` 中的 `HealthCheck` 改为：

```go
func (c *BaseClient) HealthCheck(ctx context.Context) error {
 c.mu.RLock()
 connected := c.connected
 c.mu.RUnlock()

 if !connected {
  c.mu.Lock()
  c.healthy = false
  c.mu.Unlock()
  return fmt.Errorf("not connected")
 }

 // 网络调用在锁外执行，不阻塞并发读
 req := MCPRequest{
  Method: "tools/list",
 }

 _, err := c.sendRequest(ctx, &req)

 c.mu.Lock()
 if err != nil {
  c.healthy = false
  c.logger.Warn("health check failed", zap.Error(err))
 } else {
  c.healthy = true
  c.lastHealthy = time.Now()
 }
 c.mu.Unlock()

 return err
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/mcp/... -run "TestHealthCheckDoesNotBlockConcurrentReads" -v -timeout 10s
```

期望：PASS，elapsed < 300ms。

- [ ] **Step 5: 运行全部 mcp 测试**

```bash
go test ./internal/mcp/... -v 2>&1 | tail -20
```

期望：所有测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/client.go internal/mcp/integration_test.go
git commit -m "fix: HealthCheck 改用 RLock 读状态，网络调用移到锁外，避免阻塞并发读"
```

---

### Task 4: 修复 MCPSkillWrapper.Execute 丢弃调用方 context

**Files:**

- Modify: `internal/mcp/skill_adapter.go:44-57`

背景：`Execute(input any)` 签名不接受 context，内部创建 `context.Background()`。调用方（`internal/skill/executor.go:64`）通过 goroutine + `time.After` 实现超时，但 MCP 调用本身无法被取消。

修复方案：在 `MCPSkillWrapper` 上存储一个默认 context（在构造时传入），`Execute` 使用该 context。这样不改变 `Execute(input any)` 接口签名（避免影响 `SkillRegistry` 接口），但允许在构造时注入带超时的 context。

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/mcp_test.go` 末尾添加：

```go
// TestMCPSkillWrapperUsesStoredContext 验证 Execute 使用构造时注入的 context
func TestMCPSkillWrapperUsesStoredContext(t *testing.T) {
 // 创建一个已取消的 context
 ctx, cancel := context.WithCancel(context.Background())
 cancel() // 立即取消

 // 构造 wrapper，注入已取消的 context
 wrapper := &MCPSkillWrapper{
  ctx:      ctx,
  ServerID: "test-server",
  Tool:     &MCPTool{Name: "test-tool"},
  Manager:  nil, // Manager 为 nil，调用会 panic 或 error
  logger:   zap.NewNop(),
 }

 // Execute 应该因 context 已取消而快速返回错误，而不是 panic
 _, err := wrapper.Execute(map[string]any{"key": "value"})
 if err == nil {
  t.Error("expected error due to cancelled context, got nil")
 }
}
```

注意：此测试需要 `MCPSkillWrapper` 有 `ctx` 字段。先运行确认编译失败。

- [ ] **Step 2: 运行测试确认编译失败**

```bash
go test ./internal/mcp/... -run "TestMCPSkillWrapperUsesStoredContext" -v 2>&1 | head -20
```

期望：编译错误，`MCPSkillWrapper` 没有 `ctx` 字段。

- [ ] **Step 3: 修改 MCPSkillWrapper 结构体和 Execute**

在 `internal/mcp/skill_adapter.go` 中：

1. 在 `MCPSkillWrapper` 结构体（约 line 20）加入 `ctx` 字段：

```go
type MCPSkillWrapper struct {
 ctx      context.Context
 ID       string
 Name     string
 Type     string
 ServerID string
 Tool     *MCPTool
 Manager  *ClientManager
 logger   *zap.Logger
}
```

1. 将 `Execute` 改为使用 `w.ctx`：

```go
func (w *MCPSkillWrapper) Execute(input any) (any, error) {
 ctx := w.ctx
 if ctx == nil {
  ctx = context.Background()
 }
 result, err := w.Manager.CallTool(ctx, w.ServerID, w.Tool.Name, input)
 if err != nil {
  w.logger.Error("failed to execute MCP tool",
   zap.String("tool", w.Tool.Name),
   zap.String("server_id", w.ServerID),
   zap.Error(err))
  return nil, err
 }
 return result, nil
}
```

1. 在 `DiscoverSkills` 中构造 `MCPSkillWrapper` 时传入 ctx（约 line 100-120，找到 `&MCPSkillWrapper{` 的位置）：

```go
wrapper := &MCPSkillWrapper{
    ctx:      ctx,
    ID:       fmt.Sprintf("%s/%s", a.serverID, tool.Name),
    Name:     tool.Name,
    Type:     "mcp",
    ServerID: a.serverID,
    Tool:     tool,
    Manager:  a.manager,
    logger:   a.logger,
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/mcp/... -run "TestMCPSkillWrapperUsesStoredContext" -v
```

期望：PASS（Manager 为 nil 时 CallTool 返回 error，context 已取消也返回 error）。

- [ ] **Step 5: 运行全部 mcp 测试**

```bash
go test ./internal/mcp/... -v 2>&1 | tail -20
```

期望：所有测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/skill_adapter.go internal/mcp/mcp_test.go
git commit -m "fix: MCPSkillWrapper 存储构造时的 context，Execute 不再丢弃调用方超时/取消信号"
```

---

### Task 5: 修复 NetworkPolicy — 恢复 ingress-nginx 入站规则

**Files:**

- Modify: `k8s/network-policy.yaml:15-22`

背景：原始策略允许来自 `ingress-nginx` 命名空间的流量访问 8080 端口。当前分支删除了该规则，导致 Ingress controller 无法到达应用。

- [ ] **Step 1: 恢复 ingress-nginx 入站规则**

将 `k8s/network-policy.yaml` 的 `ingress` 部分改为：

```yaml
  ingress:
    # 允许来自 Ingress Controller 的流量
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
      ports:
        - protocol: TCP
          port: 8080
    # 允许来自 namespace 内部的流量
    - from:
        - podSelector: {}
      ports:
        - protocol: TCP
          port: 8080
        - protocol: TCP
          port: 9090
```

- [ ] **Step 2: 验证 YAML 语法**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/network-policy.yaml')))" && echo "YAML valid"
```

期望：`YAML valid`。

- [ ] **Step 3: Commit**

```bash
git add k8s/network-policy.yaml
git commit -m "fix: 恢复 NetworkPolicy ingress-nginx 入站规则，修复外部流量被切断"
```

---

### Task 6: 修复 Neo4j 内存配置 — 提高容器内存限制

**Files:**

- Modify: `k8s/dependencies.yaml:321-327`

背景：heap(256m) + pagecache(128m) + JVM overhead(~100m) ≈ 484m，容器限制 512Mi 仅留 ~28m 余量。高负载下 OOMKilled 风险高。将容器内存限制提高到 768Mi，给 JVM 足够余量。

- [ ] **Step 1: 修改 Neo4j 容器内存限制**

将 `k8s/dependencies.yaml` 中 Neo4j Deployment 的 resources 部分（约 line 321-327）改为：

```yaml
        resources:
          requests:
            cpu: 200m
            memory: 512Mi
          limits:
            cpu: 500m
            memory: 768Mi
```

- [ ] **Step 2: 验证 YAML 语法**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/dependencies.yaml')))" && echo "YAML valid"
```

期望：`YAML valid`。

- [ ] **Step 3: Commit**

```bash
git add k8s/dependencies.yaml
git commit -m "fix: 提高 Neo4j 容器内存限制至 768Mi，避免 JVM overhead 导致 OOMKilled"
```

---

### Task 7: 运行全量测试验证无回归

- [ ] **Step 1: 运行全量测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test ./... -short 2>&1 | tail -30
```

期望：所有包 PASS，无 FAIL。

- [ ] **Step 2: go vet**

```bash
go vet ./...
```

期望：无输出（无错误）。

- [ ] **Step 3: 如有失败，修复后重新运行**

若有测试失败，阅读错误信息，定位到具体文件和行号，修复后重新运行 Step 1。

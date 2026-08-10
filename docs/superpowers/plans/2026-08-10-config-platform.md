# 配置平台化（Nacos）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 引入 Nacos 2.x 配置中心，档位 A 配置（业务/功能）从 Nacos 加载、热生效、可审计；开启 `MEMORY_PIPELINE_ENABLED` 消化堆积记忆。

**Architecture:** 三层配置模型——Nacos（档位 A）→ config 层（Nacos 优先、env 兜底）→ configmap/secret（档位 C 不动）。nacos-sdk-go v2 提供 gRPC 推送；冷生效字段（Enabled 等装配参数）启动时拉取覆盖，热生效字段（OutboxPoller 的 PollInterval/BatchSize）通过 `atomic.Pointer` + listener 回调每轮生效。

**Tech Stack:** Go 1.25、nacos-sdk-go v2（新增依赖）、Nacos server v2.5 standalone + MySQL 8（k3s 远端）、Prometheus/Zap 现有。

## Global Constraints

- 档位边界（spec §4.2）：档位 B（表现域：prompt/模型/温度/阈值）不进 Nacos，保持 env；档位 C（连接串/密钥）永不经过 Nacos。
- 依赖方向：`config` 包不 import `internal/`；`pipeline`（internal/memory/infrastructure）不 import `config`——类型转换只在 `api/wiring` 做薄适配。
- fail-closed：Nacos 不可达 → WARN + env/默认值启动（不阻塞、不静默）；运行中断连保留 last-known；非法值回退旧值并告警。
- 日志只用 Zap；错误用 `fmt.Errorf("op: %w", err)`；行为数字走 `pkg/constants`。
- 圈复杂度 ≤10、函数 ≤120 行、嵌套 ≤4；`go vet && go test -short ./...` 绿后方可提交。
- commit 格式 `[type](scope): description`；禁止 main 直接提交。

---

### Task 1: pipeline 动态配置（OutboxPoller 热更新）

**Files:**

- Modify: `internal/memory/infrastructure/pipeline/config.go`
- Modify: `internal/memory/infrastructure/pipeline/outbox_poller.go`
- Test: `internal/memory/infrastructure/pipeline/outbox_poller_test.go`（新增测试函数，不动现有用例）

**Interfaces:**

- Consumes: 现有 `pipeline.Config`（`PollInterval time.Duration`、`BatchSize int` 字段）。
- Produces:
  - `type DynamicConfig struct { PollInterval time.Duration; BatchSize int }`
  - `func (p *OutboxPoller) WithDynamic(d *atomic.Pointer[DynamicConfig]) *OutboxPoller` —— 链式 setter（仿现有 `WithEmbedResolver` 模式），`d == nil` 时行为与现状完全一致。

- [ ] **Step 1: 写失败测试**

在 `outbox_poller_test.go` 追加：

```go
func TestOutboxPollerDynamicConfigOverridesStatic(t *testing.T) {
 p := &OutboxPoller{interval: time.Second, batch: 10}
 var dyn atomic.Pointer[DynamicConfig]
 dyn.Store(&DynamicConfig{PollInterval: 2 * time.Second, BatchSize: 20})
 p.WithDynamic(&dyn)

 if got := p.currentInterval(); got != 2*time.Second {
  t.Fatalf("currentInterval() = %v, want 2s", got)
 }
 if got := p.currentBatch(); got != 20 {
  t.Fatalf("currentBatch() = %d, want 20", got)
 }
}

func TestOutboxPollerDynamicConfigZeroValueFallsBackToStatic(t *testing.T) {
 p := &OutboxPoller{interval: time.Second, batch: 10}
 var dyn atomic.Pointer[DynamicConfig]
 p.WithDynamic(&dyn) // 指针非 nil，但未 Store 过

 if got := p.currentInterval(); got != time.Second {
  t.Fatalf("currentInterval() = %v, want static 1s", got)
 }
 if got := p.currentBatch(); got != 10 {
  t.Fatalf("currentBatch() = %d, want static 10", got)
 }
}
```

（`atomic` 需 `"sync/atomic"` import；`OutboxPoller` 字段可直接构造——同包测试。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/infrastructure/pipeline/ -run TestOutboxPollerDynamic -v`
Expected: FAIL —— `currentInterval`/`currentBatch`/`WithDynamic` undefined。

- [ ] **Step 3: 实现**

`internal/memory/infrastructure/pipeline/config.go` 追加：

```go
// DynamicConfig 是运行期可热更新的 pipeline 调度参数。
// 由 Nacos listener 经 wiring 桥接后原子替换；零值字段回退静态 Config。
type DynamicConfig struct {
 PollInterval time.Duration
 BatchSize    int
}
```

`outbox_poller.go`：

```go
type OutboxPoller struct {
 pool     *pgxpool.Pool
 js       jetstream.JetStream
 logger   *zap.Logger
 interval time.Duration
 batch    int
 // dynamic 提供运行期可变的调度参数；nil 时回退 interval/batch 静态值。
 dynamic  *atomic.Pointer[DynamicConfig]
 stopCh   chan struct{}
 stopOnce sync.Once
 begin    func(context.Context) (pgx.Tx, error)
}

// WithDynamic 挂载热更新配置源。d 为 nil 时 poller 完全按静态值运行。
func (p *OutboxPoller) WithDynamic(d *atomic.Pointer[DynamicConfig]) *OutboxPoller {
 p.dynamic = d
 return p
}

func (p *OutboxPoller) currentInterval() time.Duration {
 if d := p.dynamic; d != nil {
  if dc := d.Load(); dc != nil && dc.PollInterval > 0 {
   return dc.PollInterval
  }
 }
 return p.interval
}

func (p *OutboxPoller) currentBatch() int {
 if d := p.dynamic; d != nil {
  if dc := d.Load(); dc != nil && dc.BatchSize > 0 {
   return dc.BatchSize
  }
 }
 return p.batch
}
```

`Start` 循环改为 interval 变化时重建 ticker、poll 用动态 batch：

```go
func (p *OutboxPoller) Start(ctx context.Context) {
 interval := p.currentInterval()
 ticker := time.NewTicker(interval)
 defer ticker.Stop()
 p.logger.Info("memory.outbox.poller_started",
  zap.Duration("interval", interval),
  zap.Int("batch", p.currentBatch()))
 for {
  select {
  case <-ctx.Done():
   p.logger.Info("memory.outbox.poller_stopped", zap.String("cause", "ctx_done"))
   return
  case <-p.stopCh:
   p.logger.Info("memory.outbox.poller_stopped", zap.String("cause", "stop_called"))
   return
  case <-ticker.C:
   if cur := p.currentInterval(); cur != interval {
    interval = cur
    ticker.Reset(interval) // 同一 goroutine 内 Reset 安全
    p.logger.Info("memory.outbox.poller_interval_changed", zap.Duration("interval", interval))
   }
   if err := p.poll(ctx); err != nil {
    p.logger.Error("memory.outbox.poll", zap.Error(err))
   }
  }
 }
}
```

`takeOutboxBatch` 中 `p.batch`（第 195 行 `LIMIT $1` 参数）改为 `p.currentBatch()`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/infrastructure/pipeline/ -v`
Expected: 全部 PASS（新增 2 个 + 现有用例不动）。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/pipeline/config.go internal/memory/infrastructure/pipeline/outbox_poller.go internal/memory/infrastructure/pipeline/outbox_poller_test.go
git commit -m "[feat](memory): OutboxPoller 支持热更新调度参数（DynamicConfig）"
```

---

### Task 2: config 包动态配置类型与回调机制

**Files:**

- Modify: `config/config.go`
- Test: `config/config_test.go`

**Interfaces:**

- Consumes: 无（纯新增）。
- Produces:
  - `type MemoryPipelineDynamic struct { PollInterval time.Duration; BatchSize int }`
  - `func (c *Config) LoadMemoryPipelineDynamic() MemoryPipelineDynamic`
  - `func (c *Config) ApplyMemoryPipelineDynamic(d MemoryPipelineDynamic)`
  - `func (c *Config) OnMemoryPipelineDynamic(fn func(MemoryPipelineDynamic))`

- [ ] **Step 1: 写失败测试**

`config/config_test.go` 追加：

```go
func TestMemoryPipelineDynamicApplyAndLoad(t *testing.T) {
 cfg := &Config{}
 d := MemoryPipelineDynamic{PollInterval: 5 * time.Second, BatchSize: 100}
 cfg.ApplyMemoryPipelineDynamic(d)
 got := cfg.LoadMemoryPipelineDynamic()
 if got != d {
  t.Fatalf("LoadMemoryPipelineDynamic() = %+v, want %+v", got, d)
 }
}

func TestMemoryPipelineDynamicListenerFires(t *testing.T) {
 cfg := &Config{}
 var got MemoryPipelineDynamic
 cfg.OnMemoryPipelineDynamic(func(d MemoryPipelineDynamic) { got = d })
 cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: 7 * time.Second})
 if got.PollInterval != 7*time.Second {
  t.Fatalf("listener got %+v, want PollInterval=7s", got)
 }
}

func TestMemoryPipelineDynamicZeroWhenUnset(t *testing.T) {
 cfg := &Config{}
 if got := cfg.LoadMemoryPipelineDynamic(); got != (MemoryPipelineDynamic{}) {
  t.Fatalf("expected zero value, got %+v", got)
 }
}
```

（`time` 已在 config_test.go 或新增 import。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./config/ -run TestMemoryPipelineDynamic -v`
Expected: FAIL —— undefined 编译错误。

- [ ] **Step 3: 实现**

`config/config.go` 追加（文件尾部、`getEnv` 之前）：

```go
// MemoryPipelineDynamic 是运行期可热更新的 memory pipeline 调度参数
// （Nacos stratum/memory dataId 的 poll_interval/batch_size），
// 与冷生效的装配参数（Enabled、worker 数等）分离。
type MemoryPipelineDynamic struct {
 PollInterval time.Duration
 BatchSize    int
}

// OnMemoryPipelineDynamic 注册热更新回调；回调在 listener goroutine 同步调用，
// 必须非阻塞、不得持有锁（内部由 wiring 做 atomic Store）。
func (c *Config) OnMemoryPipelineDynamic(fn func(MemoryPipelineDynamic)) {
 c.dynamicMu.Lock()
 c.memoryDynamicListeners = append(c.memoryDynamicListeners, fn)
 c.dynamicMu.Unlock()
}

// ApplyMemoryPipelineDynamic 原子写入动态配置并通知所有 listener。
func (c *Config) ApplyMemoryPipelineDynamic(d MemoryPipelineDynamic) {
 c.memoryDynamic.Store(&d)
 c.dynamicMu.RLock()
 listeners := append([]func(MemoryPipelineDynamic){}, c.memoryDynamicListeners...)
 c.dynamicMu.RUnlock()
 for _, fn := range listeners {
  fn(d)
 }
}

// LoadMemoryPipelineDynamic 返回当前动态值；未设置过时为零值。
func (c *Config) LoadMemoryPipelineDynamic() MemoryPipelineDynamic {
 if d := c.memoryDynamic.Load(); d != nil {
  return *d
 }
 return MemoryPipelineDynamic{}
}
```

`Config` struct 追加字段：

```go
 MemoryPipeline          MemoryPipelineConfig
 // 热更新运行时状态（Nacos listener 写、wiring 注册回调读）
 memoryDynamic           atomic.Pointer[MemoryPipelineDynamic]
 memoryDynamicListeners  []func(MemoryPipelineDynamic)
 dynamicMu               sync.RWMutex
```

（`config.go` import 追加 `"sync"`、`"sync/atomic"`。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./config/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "[feat](config): MemoryPipelineDynamic 动态配置类型与 listener 回调"
```

---

### Task 3: Nacos 客户端封装（nacos-sdk-go v2）

**Files:**

- Create: `config/nacos.go`
- Test: `config/nacos_test.go`

**Interfaces:**

- Consumes: `Config` 新增字段（Step 3 定义）：`NacosURL/NacosNamespace/NacosUsername/NacosPassword string`（`Load()` 从 `NACOS_URL/NACOS_NAMESPACE/NACOS_USERNAME/NACOS_PASSWORD` 读取，默认 `""`）。
- Produces:
  - `type NacosSettings struct { URL, Namespace, Username, Password string }`
  - `func (s NacosSettings) ServerAddresses() (host string, port uint64, scheme string, err error)`
  - `func newNacosClient(s NacosSettings) (nacosClient, error)`
  - `type nacosClient interface { GetConfig(dataID string) (string, error); Listen(dataID string, onChange func(content string)) error; Close() error }`
  - 包级 `nacosGroup = "DEFAULT_GROUP"`、`nacosAuthDataID = "stratum/auth"`、`nacosMemoryDataID = "stratum/memory"`

- [ ] **Step 1: 写失败测试**

`config/nacos_test.go`：

```go
package config

import "testing"

func TestNacosSettingsServerAddresses(t *testing.T) {
 cases := []struct {
  name string
  url  string
  host string
  port uint64
  scheme string
 }{
  {name: "http with port", url: "http://nacos:8848", host: "nacos", port: 8848, scheme: "http"},
  {name: "no scheme defaults http", url: "nacos:8848", host: "nacos", port: 8848, scheme: "http"},
  {name: "host only defaults 8848", url: "nacos", host: "nacos", port: 8848, scheme: "http"},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   s := NacosSettings{URL: tc.url}
   host, port, scheme, err := s.ServerAddresses()
   if err != nil {
    t.Fatalf("ServerAddresses() error: %v", err)
   }
   if host != tc.host || port != tc.port || scheme != tc.scheme {
    t.Fatalf("got %s:%d(%s), want %s:%d(%s)", host, port, scheme, tc.host, tc.port, tc.scheme)
   }
  })
 }
}

func TestNacosSettingsServerAddressesInvalid(t *testing.T) {
 s := NacosSettings{URL: "http://host:notaport"}
 if _, _, _, err := s.ServerAddresses(); err == nil {
  t.Fatal("expected error for invalid port")
 }
}

// fakeNacosClient 用于 config 包内测试，不依赖真实 Nacos。
type fakeNacosClient struct {
 contents map[string]string
 listeners map[string]func(string)
 err       error
}

func (f *fakeNacosClient) GetConfig(dataID string) (string, error) {
 if f.err != nil {
  return "", f.err
 }
 return f.contents[dataID], nil
}

func (f *fakeNacosClient) Listen(dataID string, onChange func(string)) error {
 if f.err != nil {
  return f.err
 }
 f.listeners[dataID] = onChange
 return nil
}

func (f *fakeNacosClient) Close() error { return nil }
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./config/ -run TestNacosSettings -v`
Expected: FAIL —— `NacosSettings` undefined。

- [ ] **Step 3: 加依赖**

```bash
go get github.com/nacos-group/nacos-sdk-go/v2@latest
go mod tidy
```

若 `go mod tidy` 与现有 grpc 版本冲突（SDK 依赖 grpc v1.x，项目经 Milvus SDK 已有 grpc）：先 `go build ./...` 验证；冲突时升级统一 grpc 版本并跑 `go test -short ./...` 全量回归后再继续。

- [ ] **Step 4: 实现**

`config/nacos.go`：

```go
package config

import (
 "fmt"
 "net/url"
 "strconv"

 "github.com/nacos-group/nacos-sdk-go/v2/clients"
 "github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
 "github.com/nacos-group/nacos-sdk-go/v2/common/constant"
 "github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// Nacos dataId 约定：按业务域拆分，变更粒度小、审计清晰。
const (
 nacosGroup      = "DEFAULT_GROUP"
 nacosAuthDataID = "stratum/auth"
 nacosMemoryDataID = "stratum/memory"
)

// nacosClient 抽象 Nacos 配置客户端，便于测试注入。
type nacosClient interface {
 GetConfig(dataID string) (string, error)
 Listen(dataID string, onChange func(content string)) error
 Close() error
}

type NacosSettings struct {
 URL       string
 Namespace string
 Username  string
 Password  string
}

// ServerAddresses 解析 URL（如 http://nacos:8848）为 SDK 需要的 host/port/scheme。
// 无 scheme 视为 http；无端口默认 8848。
func (s NacosSettings) ServerAddresses() (host string, port uint64, scheme string, err error) {
 raw := s.URL
 if !containsScheme(raw) {
  raw = "http://" + raw
 }
 u, err := url.Parse(raw)
 if err != nil {
  return "", 0, "", fmt.Errorf("nacos url parse: %w", err)
 }
 if u.Hostname() == "" {
  return "", 0, "", fmt.Errorf("nacos url missing host: %s", s.URL)
 }
 scheme = u.Scheme
 if scheme != "http" && scheme != "https" {
  return "", 0, "", fmt.Errorf("nacos url scheme must be http/https: %s", s.URL)
 }
 host = u.Hostname()
 port = 8848
 if p := u.Port(); p != "" {
  parsed, perr := strconv.ParseUint(p, 10, 16)
  if perr != nil {
   return "", 0, "", fmt.Errorf("nacos url port: %w", perr)
  }
  port = parsed
 }
 return host, port, scheme, nil
}

func containsScheme(raw string) bool {
 for i := 0; i < len(raw); i++ {
  if raw[i] == ':' {
   return i > 0
  }
  if raw[i] == '/' {
   return false
  }
 }
 return false
}

type sdkNacosClient struct {
 cc config_client.IConfigClient
}

// newNacosClient 构造 Nacos 客户端；包级变量便于测试注入 fake（Task 4）。
var newNacosClient = newNacosClientImpl

func newNacosClientImpl(s NacosSettings) (nacosClient, error) {
 host, port, scheme, err := s.ServerAddresses()
 if err != nil {
  return nil, err
 }
 cc, err := clients.NewConfigClient(clients.NacosClientParam{
  ClientConfig: &constant.ClientConfig{
   NamespaceId:  s.Namespace,
   Username:     s.Username,
   Password:     s.Password,
   TimeoutMs:    uint64(constants.NacosTimeoutMs),
   LogLevel:     "warn",
   NotLoadCacheAtStart: true,
  },
  ServerConfigs: []constant.ServerConfig{
   {IpAddr: host, Port: port, Scheme: scheme},
  },
 })
 if err != nil {
  return nil, fmt.Errorf("nacos client: %w", err)
 }
 return &sdkNacosClient{cc: cc}, nil
}

func (c *sdkNacosClient) GetConfig(dataID string) (string, error) {
 content, err := c.cc.GetConfig(vo.ConfigParam{DataId: dataID, Group: nacosGroup})
 if err != nil {
  return "", fmt.Errorf("nacos get %s: %w", dataID, err)
 }
 return content, nil
}

func (c *sdkNacosClient) Listen(dataID string, onChange func(string)) error {
 if err := c.cc.ListenConfig(vo.ConfigParam{
  DataId:   dataID,
  Group:    nacosGroup,
  OnChange: func(namespace, group, dataId, data string) { onChange(data) },
 }); err != nil {
  return fmt.Errorf("nacos listen %s: %w", dataID, err)
 }
 return nil
}

func (c *sdkNacosClient) Close() error {
 if err := c.cc.CloseClient(); err != nil {
  return fmt.Errorf("nacos close: %w", err)
 }
 return nil
}
```

新建 `pkg/constants/config.go`：

```go
// NacosTimeoutMs 是 Nacos 客户端请求超时（毫秒）。
const NacosTimeoutMs = 3000
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./config/ -v && go build ./...`
Expected: 全部 PASS；编译通过（SDK 依赖已就位）。

- [ ] **Step 6: Commit**

```bash
git add config/nacos.go config/nacos_test.go config/config.go pkg/constants/ go.mod go.sum
git commit -m "[feat](config): Nacos SDK 客户端封装（GetConfig/Listen/Close）"
```

---

### Task 4: ConnectNacos——冷生效覆盖 + 热更新注册 + fail-closed

**Files:**

- Modify: `config/config.go`（`Config` struct 加 Nacos 字段 + `ConnectNacos`/`CloseNacos` + `applyAuthConfig`/`applyMemoryConfig`）
- Test: `config/nacos_test.go`（追加）

**Interfaces:**

- Consumes: Task 3 的 `nacosClient`/`NacosSettings`/`nacosGroup`/`nacosAuthDataID`/`nacosMemoryDataID`；Task 2 的 `ApplyMemoryPipelineDynamic`。
- Produces:
  - `func (c *Config) ConnectNacos(logger *zap.Logger) error` —— 未配置 `NacosURL` 时返回 nil（不启用）；连接失败返回 error（main WARN，fail-closed）；成功时同步拉取冷生效字段并注册热更新 listener。
  - `func (c *Config) CloseNacos() error` —— 幂等，未连接时返回 nil。
  - `func (c *Config) applyAuthConfig(content string) error`
  - `func (c *Config) applyMemoryConfig(content string) error`

- [ ] **Step 1: 写失败测试**

`config/nacos_test.go` 追加：

```go
func TestConnectNacosAppliesColdAndHotConfig(t *testing.T) {
 fake := &fakeNacosClient{
  contents: map[string]string{
   nacosAuthDataID:   `{"password_auth_enabled": false, "guest_auth_enabled": true}`,
   nacosMemoryDataID: `{"enabled": true, "poll_interval": "5s", "batch_size": 100}`,
  },
  listeners: map[string]func(string){},
 }
 cfg := &Config{PasswordAuthEnabled: true, GuestAuthEnabled: false}
 newNacosClient = func(s NacosSettings) (nacosClient, error) { return fake, nil }
 defer func() { newNacosClient = newNacosClientImpl }()

 logger := zap.NewNop()
 if err := cfg.ConnectNacos(logger); err != nil {
  t.Fatalf("ConnectNacos() error: %v", err)
 }
 // 冷生效字段（启动拉取）
 if cfg.PasswordAuthEnabled {
  t.Fatal("PasswordAuthEnabled should be false from Nacos")
 }
 if !cfg.GuestAuthEnabled {
  t.Fatal("GuestAuthEnabled should be true from Nacos")
 }
 if !cfg.MemoryPipeline.Enabled {
  t.Fatal("MemoryPipeline.Enabled should be true from Nacos")
 }
 // 热更新字段（初始拉取也写入 dynamic）
 if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 5*time.Second || d.BatchSize != 100 {
  t.Fatalf("dynamic = %+v, want 5s/100", d)
 }
 // 热更新推送
 cfg.applyMemoryConfig(`{"poll_interval": "10s", "batch_size": 200}`)
 if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 10*time.Second || d.BatchSize != 200 {
  t.Fatalf("dynamic after push = %+v, want 10s/200", d)
 }
}

func TestConnectNacosSkippedWhenNotConfigured(t *testing.T) {
 cfg := &Config{} // NacosURL 为空
 if err := cfg.ConnectNacos(zap.NewNop()); err != nil {
  t.Fatalf("ConnectNacos() = %v, want nil skip", err)
 }
}

func TestApplyMemoryConfigInvalidJSONKeepsOldValue(t *testing.T) {
 cfg := &Config{}
 cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: time.Second})
 if err := cfg.applyMemoryConfig(`{not json`); err == nil {
  t.Fatal("expected error for invalid JSON")
 }
 if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != time.Second {
  t.Fatalf("dynamic lost after invalid push: %+v", d)
 }
}

func TestApplyMemoryConfigInvalidDurationKeepsOldValue(t *testing.T) {
 cfg := &Config{}
 cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: time.Second, BatchSize: 10})
 if err := cfg.applyMemoryConfig(`{"poll_interval": "5x", "batch_size": 20}`); err == nil {
  t.Fatal("expected error for invalid duration")
 }
 // 原子性：整体回退，batch_size 也不应用
 if d := cfg.LoadMemoryPipelineDynamic(); d.BatchSize != 10 {
  t.Fatalf("partial apply detected: %+v", d)
 }
}

func TestApplyMemoryConfigMissingDynamicFieldsDontClear(t *testing.T) {
 cfg := &Config{}
 cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: 3 * time.Second, BatchSize: 50})
 // 只改 enabled，不提供调度字段 → 动态值保留
 if err := cfg.applyMemoryConfig(`{"enabled": true}`); err != nil {
  t.Fatalf("applyMemoryConfig() error: %v", err)
 }
 if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 3*time.Second || d.BatchSize != 50 {
  t.Fatalf("dynamic cleared by partial push: %+v", d)
 }
}

func TestApplyAuthConfigMissingFieldsDontOverride(t *testing.T) {
 cfg := &Config{PasswordAuthEnabled: true}
 // 字段缺省（JSON 无 password_auth_enabled）→ 保持 env 值
 if err := cfg.applyAuthConfig(`{"guest_auth_enabled": false}`); err != nil {
  t.Fatalf("applyAuthConfig() error: %v", err)
 }
 if !cfg.PasswordAuthEnabled {
  t.Fatal("missing field should not override existing value")
 }
 if cfg.GuestAuthEnabled {
  t.Fatal("guest_auth_enabled should be false")
 }
}
```

（`zap` 需 import `"go.uber.org/zap"`。新增包级可替换变量：`var newNacosClient = func(s NacosSettings) (nacosClient, error) { return defaultNewNacosClient(s) }`——见 Step 3 实现，把 Task 3 的 `newNacosClient` 改为可变包级变量。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./config/ -run TestConnectNacos -v`
Expected: FAIL —— `ConnectNacos` undefined。

- [ ] **Step 3: 实现**

`config/config.go`——`Config` struct 追加：

```go
 // Nacos 配置中心（档位 A 业务/功能配置）。未配置 NacosURL 时整个
 // Nacos 层不启用，行为与现状完全一致。
 NacosURL      string
 NacosNamespace string
 NacosUsername string
 NacosPassword string
```

`Load()` 追加（MemoryPipeline 块之后、return 之前）：

```go
  cfg.NacosURL = getEnv("NACOS_URL", "")
  cfg.NacosNamespace = getEnv("NACOS_NAMESPACE", "")
  cfg.NacosUsername = getEnv("NACOS_USERNAME", "")
  cfg.NacosPassword = getEnv("NACOS_PASSWORD", "")
```

`config/nacos.go` 追加（函数放在 `Close` 之后）：

```go
// ConnectNacos 建立 Nacos 连接并应用档位 A 配置。
// 语义（fail-closed）：未配置 NacosURL → 返回 nil 不启用；
// 连接失败 → 返回 error（调用方 WARN，用 env/默认值启动）；
// 单个 dataId 拉取/解析失败 → WARN 跳过，不阻断其余 dataId；
// 非法内容 → 整体回退旧值。
// 冷生效字段（装配参数）在同步阶段应用；热更新字段经 listener 原子写入。
func (c *Config) ConnectNacos(logger *zap.Logger) error {
 if c.NacosURL == "" {
  return nil
 }
 client, err := newNacosClient(NacosSettings{
  URL: c.NacosURL, Namespace: c.NacosNamespace,
  Username: c.NacosUsername, Password: c.NacosPassword,
 })
 if err != nil {
  return fmt.Errorf("config nacos connect: %w", err)
 }
 c.nacos = client

 for dataID, apply := range map[string]func(string) error{
  nacosAuthDataID:   c.applyAuthConfig,
  nacosMemoryDataID: c.applyMemoryConfig,
 } {
  content, err := client.GetConfig(dataID)
  if err != nil {
   logger.Warn("config: nacos get failed, using env/fallback",
    zap.String("data_id", dataID), zap.Error(err))
   continue
  }
  if err := apply(content); err != nil {
   logger.Warn("config: nacos apply failed, keeping previous value",
    zap.String("data_id", dataID), zap.Error(err))
  }
 }

 for dataID, apply := range map[string]func(string) error{
  nacosAuthDataID:   c.applyAuthConfig,
  nacosMemoryDataID: c.applyMemoryConfig,
 } {
  dataID, apply := dataID, apply // 循环变量捕获（Go <1.22 兼容）
  if err := client.Listen(dataID, func(content string) {
   if err := apply(content); err != nil {
    logger.Warn("config: nacos push rejected, keeping previous value",
     zap.String("data_id", dataID), zap.Error(err))
   }
  }); err != nil {
   logger.Warn("config: nacos listen failed, hot reload disabled for this dataId",
    zap.String("data_id", dataID), zap.Error(err))
  }
 }
 return nil
}

// CloseNacos 关闭 Nacos 连接。幂等。
func (c *Config) CloseNacos() error {
 if c.nacos == nil {
  return nil
 }
 if err := c.nacos.Close(); err != nil {
  return fmt.Errorf("config nacos close: %w", err)
 }
 c.nacos = nil
 return nil
}

// applyAuthConfig 应用 stratum/auth dataId。
// 字段缺省不覆盖（*bool 指针区分"未设置"与"显式 false"）。
func (c *Config) applyAuthConfig(content string) error {
 var d struct {
  PasswordAuthEnabled *bool `json:"password_auth_enabled"`
  GuestAuthEnabled    *bool `json:"guest_auth_enabled"`
 }
 if err := json.Unmarshal([]byte(content), &d); err != nil {
  return fmt.Errorf("parse auth config: %w", err)
 }
 if d.PasswordAuthEnabled != nil {
  c.PasswordAuthEnabled = *d.PasswordAuthEnabled
 }
 if d.GuestAuthEnabled != nil {
  c.GuestAuthEnabled = *d.GuestAuthEnabled
 }
 return nil
}

// applyMemoryConfig 应用 stratum/memory dataId。
// enabled 等装配参数为冷生效（写入字段，下次启动生效）；
// poll_interval/batch_size 为热生效（原子写入 dynamic 并通知 listener）。
// 任一字段非法 → 整体回退（不部分应用）。
func (c *Config) applyMemoryConfig(content string) error {
 var d struct {
  Enabled      *bool   `json:"enabled"`
  PollInterval string  `json:"poll_interval"`
  BatchSize    *int    `json:"batch_size"`
 }
 if err := json.Unmarshal([]byte(content), &d); err != nil {
  return fmt.Errorf("parse memory config: %w", err)
 }
 dynamic := MemoryPipelineDynamic{}
 if d.PollInterval != "" {
  parsed, err := time.ParseDuration(d.PollInterval)
  if err != nil {
   return fmt.Errorf("parse poll_interval: %w", err)
  }
  dynamic.PollInterval = parsed
 }
 if d.BatchSize != nil {
  dynamic.BatchSize = *d.BatchSize
 }
 if d.Enabled != nil {
  c.MemoryPipeline.Enabled = *d.Enabled
 }
 // 只有显式提供调度字段时才推送动态值，缺省不覆盖（防止只改 enabled 时清零动态值）。
 if dynamic != (MemoryPipelineDynamic{}) {
  c.ApplyMemoryPipelineDynamic(dynamic)
 }
 return nil
}
```

`Config` struct 追加 unexported 字段：

```go
 // nacos 客户端实例（ConnectNacos 设置、CloseNacos 清空）
 nacos nacosClient
```

`config/nacos.go` 头部 import 追加 `"encoding/json"`、`"time"`、`"go.uber.org/zap"`、`pkg/constants`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./config/ -v`
Expected: 全部 PASS（含 Task 2/3 用例）。

- [ ] **Step 5: Commit**

```bash
git add config/ pkg/constants/
git commit -m "[feat](config): ConnectNacos 冷生效覆盖与热更新注册（fail-closed）"
```

---

### Task 5: main 接线 + wiring 热更新管道

**Files:**

- Modify: `cmd/server/main.go`
- Modify: `api/wiring/memory.go`（`buildMemoryPipeline`）
- Modify: `internal/memory/infrastructure/pipeline/pipeline.go`（`WithDynamic` setter + `Start` 挂载 poller）

**Interfaces:**

- Consumes: Task 4 `ConnectNacos/CloseNacos`；Task 1 `DynamicConfig`；Task 2 `OnMemoryPipelineDynamic/LoadMemoryPipelineDynamic`。
- Produces: 无（装配完成）。

- [ ] **Step 1: 实现**

`cmd/server/main.go`——`Load()` 与 `BuildContainer` 之间（logger 创建之后）：

```go
 // 配置中心（Nacos）：档位 A 业务/功能配置，Nacos 优先、env 兜底。
 // fail-closed：连接失败 WARN 后按 env/默认值启动，不阻断。
 if err := cfg.ConnectNacos(logger); err != nil {
  logger.Warn("config: nacos unavailable, using env/fallback config", zap.Error(err))
 } else {
  defer func() { _ = cfg.CloseNacos() }()
 }
```

（必须位于 `wiring.BuildContainer` 之前——`MemoryPipeline.Enabled` 影响装配。）

`internal/memory/infrastructure/pipeline/pipeline.go`：

```go
// WithDynamic 挂载热更新调度参数源（Nacos 经 wiring 桥接）。
// 必须在 Start 之前调用。
func (p *Pipeline) WithDynamic(d *atomic.Pointer[DynamicConfig]) *Pipeline {
 p.dynamic = d
 return p
}
```

`Pipeline` struct 加字段 `dynamic *atomic.Pointer[DynamicConfig]`；`Start` 中 poller 构造后挂载：

```go
 p.poller = NewOutboxPoller(p.pool, js, p.logger, p.cfg)
 if p.dynamic != nil {
  p.poller.WithDynamic(p.dynamic)
 }
```

`api/wiring/memory.go` `buildMemoryPipeline`（`p := pipeline.New(...)` 之后）：

```go
 // 热更新管道：config 层动态配置 → atomic 指针 → poller 每轮 re-read。
 var dynamic atomic.Pointer[pipeline.DynamicConfig]
 if d := c.Config.LoadMemoryPipelineDynamic(); d.PollInterval > 0 || d.BatchSize > 0 {
  dynamic.Store(&pipeline.DynamicConfig{PollInterval: d.PollInterval, BatchSize: d.BatchSize})
 }
 c.Config.OnMemoryPipelineDynamic(func(d config.MemoryPipelineDynamic) {
  dynamic.Store(&pipeline.DynamicConfig{PollInterval: d.PollInterval, BatchSize: d.BatchSize})
 })
 p.WithDynamic(&dynamic)
```

（import 追加 `"sync/atomic"` 与 `config` 包。注意：`dynamic` 若从未 Store 过，poller 回退静态值——与现状一致。）

- [ ] **Step 2: 构建与全量快速验证**

Run: `go vet ./... && go test -short ./...`
Expected: 全部 PASS（`contract_test.go` 的 `Load()` 签名未变，不受影响）。

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go api/wiring/memory.go internal/memory/infrastructure/pipeline/pipeline.go
git commit -m "[feat](wiring): main 接入 ConnectNacos，pipeline 热更新管道桥接"
```

---

### Task 6: k8s 部署资源（Nacos + MySQL）

**Files:**

- Create: `k8s/nacos-mysql.yaml`
- Create: `k8s/nacos.yaml`
- Modify: `k8s/deployment.yaml`（NACOS_* env）
- Modify: `k8s/configmap.yaml`（NACOS_URL/NACOS_NAMESPACE/NACOS_USERNAME 键）
- Modify: `k8s/secret.example.yaml`（NACOS_PASSWORD 键说明）

**Interfaces:**

- Consumes: 无（纯部署清单）。
- Produces: 远端可应用的 Nacos 部署（`kubectl apply`）。

- [ ] **Step 1: 写 `k8s/nacos-mysql.yaml`**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: nacos-mysql
  namespace: stratum
stringData:
  password: "CHANGE_ME_NACOS_MYSQL"
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nacos-mysql-data
  namespace: stratum
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 5Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nacos-mysql
  namespace: stratum
spec:
  replicas: 1
  selector:
    matchLabels: {app: nacos-mysql}
  template:
    metadata:
      labels: {app: nacos-mysql}
    spec:
      containers:
        - name: mysql
          image: mysql:8.4
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef: {name: nacos-mysql, key: password}
            - name: MYSQL_DATABASE
              value: nacos
          ports: [{containerPort: 3306}]
          volumeMounts:
            - name: data
              mountPath: /var/lib/mysql
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: nacos-mysql-data
---
apiVersion: v1
kind: Service
metadata:
  name: nacos-mysql
  namespace: stratum
spec:
  selector: {app: nacos-mysql}
  ports:
    - {port: 3306, targetPort: 3306}
```

- [ ] **Step 2: 写 `k8s/nacos.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nacos
  namespace: stratum
spec:
  replicas: 1
  selector:
    matchLabels: {app: nacos}
  template:
    metadata:
      labels: {app: nacos}
    spec:
      containers:
        - name: nacos
          image: nacos/nacos-server:v2.5.1
          env:
            - name: MODE
              value: standalone
            - name: MYSQL_SERVICE_HOST
              value: nacos-mysql
            - name: MYSQL_SERVICE_PORT
              value: "3306"
            - name: MYSQL_SERVICE_DB_NAME
              value: nacos
            - name: MYSQL_SERVICE_USER
              value: root
            - name: MYSQL_SERVICE_PASSWORD
              valueFrom:
                secretKeyRef: {name: nacos-mysql, key: password}
            # 认证开启（v2.5 默认开启需显式 identity key）
            - name: NACOS_AUTH_ENABLE
              value: "true"
            - name: NACOS_AUTH_IDENTITY_KEY
              value: CHANGE_ME_IDENTITY_KEY
            - name: NACOS_AUTH_IDENTITY_VALUE
              value: CHANGE_ME_IDENTITY_VALUE
            - name: NACOS_AUTH_TOKEN
              value: "CHANGE_ME_BASE64_32BYTES_OR_LONGER"
          ports:
            - {containerPort: 8848}   # 客户端 HTTP
            - {containerPort: 9848}   # 客户端 gRPC（SDK 必需）
          volumeMounts:
            - name: data
              mountPath: /home/nacos/data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: nacos-data
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nacos-data
  namespace: stratum
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 2Gi
---
apiVersion: v1
kind: Service
metadata:
  name: nacos
  namespace: stratum
spec:
  type: NodePort
  selector: {app: nacos}
  ports:
    - {name: http, port: 8848, targetPort: 8848, nodePort: 32088}
    - {name: grpc, port: 9848, targetPort: 9848, nodePort: 32098}
```

- [ ] **Step 3: 修改部署 env**

`k8s/deployment.yaml` env 段追加（configMapKeyRef 模式，与现有条目一致）：

```yaml
            - name: NACOS_URL
              valueFrom:
                configMapKeyRef:
                  name: stratum-config
                  key: NACOS_URL
            - name: NACOS_NAMESPACE
              valueFrom:
                configMapKeyRef:
                  name: stratum-config
                  key: NACOS_NAMESPACE
            - name: NACOS_USERNAME
              valueFrom:
                configMapKeyRef:
                  name: stratum-config
                  key: NACOS_USERNAME
            - name: NACOS_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: stratum-secrets
                  key: NACOS_PASSWORD
```

`k8s/configmap.yaml` 追加（注意与远端实际 configmap 同步）：

```yaml
  NACOS_URL: "http://nacos:8848"
  NACOS_NAMESPACE: "stratum-prod"
  NACOS_USERNAME: "nacos"
```

`k8s/secret.example.yaml` 追加键（若远端 secret 名不同，部署时以实际为准）：

```yaml
NACOS_PASSWORD: "<nacos 登录密码>"
```

- [ ] **Step 4: 本地校验 YAML**

Run: `kubectl apply --dry-run=client -f k8s/nacos-mysql.yaml -f k8s/nacos.yaml`
Expected: 无错误（dry-run 不落库）。

- [ ] **Step 5: Commit**

```bash
git add k8s/
git commit -m "[feat](deploy): Nacos standalone + MySQL 部署清单与 stratum-server env"
```

---

### Task 7: 远端部署与堆积记忆消化验收（需用户许可）

> **写操作门槛**：本任务全部步骤为远端写操作（kubectl apply/restart、Nacos 配置创建、secret 更新）。执行前必须逐条获得用户许可。

**Files:**

- 无本地代码改动（验收失败则回 Task 6 修复）。

**Interfaces:**

- Consumes: Task 6 的 yaml；远端 `stratum-config` configmap / secret（实际状态以 kubectl 为准）。

- [ ] **Step 1: 部署 MySQL 与 Nacos**

Run: `ssh root@101.200.181.141 "kubectl apply -f -" <<EOF < k8s/nacos-mysql.yaml k8s/nacos.yaml EOF`（经 scp 后 apply，或逐文件 apply）
Run: `ssh root@101.200.181.141 "kubectl -n stratum rollout status deploy/nacos-mysql deploy/nacos"`
Expected: 两个 Deployment Ready；`kubectl -n stratum get pods | grep nacos` 均为 Running。

- [ ] **Step 2: Nacos 初始化（UI 或 API）**

通过 `http://101.200.181.141:32088/nacos`（NodePort）登录（admin / 部署时设置密码）：

1. 修改默认 admin 密码。
2. 创建 namespace `stratum-prod`。
3. 建配置（group `DEFAULT_GROUP`）：
   - `stratum/auth`：`{"password_auth_enabled": true, "guest_auth_enabled": true}`
   - `stratum/memory`：`{"enabled": true, "poll_interval": "5s", "batch_size": 100}`
4. 创建只读业务账号 `nacos`（或直接用 admin 写 `NACOS_USERNAME`）。

- [ ] **Step 3: 更新远端配置并重启 stratum-server**

Run: 更新远端 `stratum-config` configmap（NACOS_URL/NACOS_NAMESPACE/NACOS_USERNAME）与 secret（NACOS_PASSWORD）→ `kubectl -n stratum rollout restart deploy/stratum-server`
Expected: pod 重启后 Running；日志出现 `memory.outbox.poller_started`（证明 `MEMORY_PIPELINE_ENABLED=true` 经 Nacos 生效）。

- [ ] **Step 4: 堆积消化验证**

Run: `kubectl -n stratum logs <stratum-server-pod> | grep -E "memory\.(outbox\.publish|embed\.success|enrich\.success)" | tail -40`
Run: `kubectl exec -n stratum <postgres-pod> -- sh -c "psql -U \$POSTGRES_USER -d stratum -c 'SELECT count(*) FROM \"tenant_<id>\".memory_outbox;'"`（以实际租户 schema 为准）
Run: 浏览器验证 `https://101.200.181.141:8443/memory` 条数 > 0。
Expected: outbox 计数递减至 0；`memory_entries` 增长；页面显示 33 条记忆（含历史堆积）。

- [ ] **Step 5: 热更新验证（可选进阶）**

在 Nacos UI 改 `stratum/memory` 的 `poll_interval` 为 `"30s"` → 观察日志 `memory.outbox.poller_interval_changed`。
Expected: 无需重启即生效。

- [ ] **Step 6: 汇总验收报告**

记录：部署产物、Nacos 配置截图/内容、堆积消化前后 outbox 计数、页面条数、热更新日志。写入 PR 描述。

---

### Task 8: 治理文档（spec §6 阶段 3）

**Files:**

- Create: `docs/config-management.md`

**Interfaces:**

- Consumes: spec §7 三档清单、§9 治理机制。
- Produces: 配置管理文档（清单 + 判定两问 + 变更流程）。

- [ ] **Step 1: 写 `docs/config-management.md`**

内容结构（中文，与 `docs/agent/` 风格一致）：

```markdown
# 配置管理

## 分层

- 档位 A：Nacos（业务/功能配置，人工管理，热生效，可审计）——namespace `stratum-prod`，group `DEFAULT_GROUP`
- 档位 B：进化链路域（DB ResourceRevision + objectstore）——表现域参数保持 env 现状，未来由进化链路接管
- 档位 C：configmap/secret（基础设施/密钥，永不进 Nacos）

## 判定两问（新配置项归档前必答）

1. 是基础设施吗？（连接串/密钥/部署 env）→ 档位 C，不进。
2. 影响 agent 表现效果吗？（prompt/模型/温度/表现阈值，可能被评测调优）→ 档位 B，不进。
3. 都否 → 档位 A，可进 Nacos。

## 配置清单

### 档位 A（Nacos dataId → 字段）

| dataId | 字段 | 热生效 | 负责人 |
|---|---|---|---|
| stratum/auth | password_auth_enabled, guest_auth_enabled | 冷（重启） | 平台 |
| stratum/memory | enabled（冷）；poll_interval, batch_size（热） | 混合 | 平台 |

### 档位 B（保持 env，未来进化链路接管）

GLOBAL_AGENT_SYSTEM_PROMPT、MEMORY_ENRICH_MODEL/SUMMARY_MODEL、
MEMORY_ENRICHMENT_PROMPT/SUMMARY_PROMPT、MEMORY_SUMMARY_TOKEN_THRESHOLD、
RERANK_*、temperature/topK 等表现类参数。

### 档位 C（configmap/secret）

全部连接串（DATABASE/NATS/REDIS/MILVUS/OPIK/S3 URL）与密钥类
（JWT_PRIVATE_KEY_PEM、DATA_ENCRYPTION_KEY、GITHUB_CLIENT_SECRET、
OPIK_API_KEY、STRATUM_ADMIN_PASSWORD、RERANK_API_KEY、
TRACE_PAYLOAD_ACCESS_KEY/SECRET_KEY）。

## 变更流程

1. 新配置项：先答判定两问 → 档位 A 才允许建 dataId。
2. 改配置：Nacos UI 操作 → 变更前截图留存 → 观察热生效日志（config: nacos push applied）。
3. 过期清理：废弃 dataId 定期盘点移除；Nacos 保留多版本回滚。
4. 责任人：每个 dataId 有负责人（上表）。
```

- [ ] **Step 2: 校验**

Run: `grep -c "档位" docs/config-management.md`
Expected: ≥ 3 处档位提及；无 markdownlint 报错（hook 自动修复后重提）。

- [ ] **Step 3: Commit**

```bash
git add docs/config-management.md
git commit -m "[docs](config): 配置管理清单与变更流程（三档划分 + 判定两问）"
```

---

## Self-Review 记录

- **Spec 覆盖**：§4.4 热生效 ✓（Task 1/2/4/5）；§4.4 fail-closed ✓（Task 4）；§5 部署 ✓（Task 6/7）；§6 阶段 1 ✓（Task 1-6）；§6 阶段 2 ✓（Task 7）；§6 阶段 3 治理文档 ✓（Task 8）。
- **档位边界**：Task 4 `applyMemoryConfig` 只处理 `enabled/poll_interval/batch_size`——档位 B（model/prompt/阈值）未暴露 ✓；密钥类未经过 Nacos ✓。
- **一致性**：`MemoryPipelineDynamic`（config）与 `DynamicConfig`（pipeline）由 wiring 转换，两类型不互相 import ✓；`Container.Config` 为 `*config.Config`（wiring.go:31），`LoadMemoryPipelineDynamic`/`OnMemoryPipelineDynamic` 指针 receiver 编译无误 ✓；`PasswordAuthEnabled`/`GuestAuthEnabled` 字段名与 config.go:20,25 一致 ✓。
- **类型链**：Task 3 `newNacosClient` 为可替换包级变量（`newNacosClientImpl` 实现），Task 4 测试注入 fake 并恢复 ✓。
- **占位符**：Task 7 Step 2 的"实际 schema/密码"、Task 6 的 `CHANGE_ME_*` 属部署期事实输入，非实现占位。

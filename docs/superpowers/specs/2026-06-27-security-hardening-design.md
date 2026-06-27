# 安全加固设计：方案 B（内网企业部署）

**日期**：2026-06-27
**范围**：Body大小限制、DTO字段约束、LLM execute限速、浏览器安全头补全
**推迟**：SSRF深度验证（`newTransport` + MCP server URL校验）作为独立后续任务

---

## Context

项目现有安全机制覆盖了身份认证和跨租户边界（JWT RS256、PostgreSQL schema隔离、角色校验、SSRF防护），但以下风险未闭环：

- `/agents/:id/execute[/stream]` 无任何限速 → 单用户可无限调用 LLM，成本失控
- 输入无大小上限 → 超大请求可打满内存/DB/LLM context window
- 浏览器安全头缺 CSP、Referrer-Policy、Permissions-Policy

---

## 变更文件

| 文件 | 操作 |
|------|------|
| `pkg/constants/api.go` | 新增：Body上限常量 |
| `api/middleware/body_limit.go` | 新增：全局 Body 大小中间件 |
| `api/middleware/middleware.go` | 修改：SecurityHeaders 补全4个头 |
| `api/middleware/error_mapping.go` | 修改：识别 `http.MaxBytesError` → 413 |
| `api/middleware/rate_limit.go` | 修改：新增 `RateLimitByKey` |
| `api/http/dto/request.go` | 修改：关键字段加 `max=` binding tag |
| `api/http/router.go` | 修改：挂载 BodyLimit、execute限速 |

---

## Section 1：全局 Body 大小限制

新文件 `api/middleware/body_limit.go`：

```go
func BodyLimit(maxBytes int64) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
        c.Next()
    }
}
```

常量 `pkg/constants/api.go`：

```go
const (
    MaxRequestBodyBytes int64 = 10 * 1024 * 1024  // 10 MB
    MaxUploadBytes      int64 = 50 * 1024 * 1024  // 50 MB（知识库上传）
)
```

`router.go` 全局挂载（在 `gin.Recovery()` 之后、业务中间件之前）：

```go
r.Use(middleware.BodyLimit(constants.MaxRequestBodyBytes))
```

知识库上传路由单独覆盖：

```go
knowledgeGroup.POST("/ingest", append(adminMW, requireActive,
    middleware.BodyLimit(constants.MaxUploadBytes),
    ragHandler.UploadDocument)...)
```

`api/middleware/error_mapping.go` 补充（在 ValidationErrors 分支之前）：

```go
var maxBytesErr *http.MaxBytesError
if errors.As(err, &maxBytesErr) {
    c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
    return
}
```

---

## Section 2：DTO 字段长度约束

`api/http/dto/request.go` 关键字段加 `max=` tag：

```go
// ExecuteAgentRequest
Query          string `json:"query"           binding:"required,max=8192"`
ConversationID string `json:"conversation_id" binding:"omitempty,max=64"`
MaxSteps       int    `json:"max_steps"       binding:"omitempty,min=1,max=20"`

// CreateAgentRequest / UpdateAgentRequest（两处同步）
Name         string `json:"name"          binding:"required,max=100"`
SystemPrompt string `json:"system_prompt" binding:"omitempty,max=16384"`
Description  string `json:"description"   binding:"omitempty,max=2000"`

// KnowledgeQueryRequest
Query string `json:"query" binding:"required,max=4096"`
TopK  int    `json:"top_k" binding:"omitempty,min=1,max=20"`
```

`max=` 对 string 校验 rune 数，对 int 校验数值上限。校验失败 gin 返回 400，`ErrorHandler` 已有 `validator.ValidationErrors` 映射，无需额外改动。

---

## Section 3：LLM Execute 端点限速

`api/middleware/rate_limit.go` 新增 `RateLimitByKey`：

```go
func RateLimitByKey(store *RateLimiterStore, keyFn func(*gin.Context) string) gin.HandlerFunc {
    return func(c *gin.Context) {
        key := keyFn(c)
        if key == "" || !store.get(key).Allow() {
            c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
            return
        }
        c.Next()
    }
}
```

限速参数常量放在 `api/middleware/rate_limit.go`（避免 `pkg/constants` 引入 `golang.org/x/time/rate`）：

```go
const (
    llmExecRate  = rate.Limit(1.0 / 3.0) // 20 req/min per user
    llmExecBurst = 3
)
```

`router.go` `registerAgents` 中（`key==""` 时拦截未注入 tenant 的请求）：

```go
execLimiter := middleware.NewRateLimiterStore(middleware.LLMExecRate, middleware.LLMExecBurst)
execRateLimit := middleware.RateLimitByKey(execLimiter, func(c *gin.Context) string {
    tid, _ := c.Get("auth.tenant_id")
    uid, _ := c.Get("auth.sub")
    return fmt.Sprintf("%v:%v", tid, uid)
})
agents.POST("/:id/execute",        requireActive, execRateLimit, agentHandler.ExecuteAgent)
agents.POST("/:id/execute/stream", requireActive, execRateLimit, agentHandler.ExecuteAgentStream)
```

> 按 tenant+user 键限速而非 IP，内网多用户共享 NAT 出口时不误伤。

---

## Section 4：安全头补全

`api/middleware/middleware.go` `SecurityHeaders()` 追加：

```go
c.Header("Content-Security-Policy",
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'")
c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
c.Header("X-XSS-Protection", "0") // 禁用旧式 XSS 过滤器，由 CSP 覆盖
```

---

## 验证

```bash
go build ./...
go test ./api/middleware/... -race

# Body limit:  curl -X POST /agents/{id}/execute -H "Authorization: Bearer $TOKEN" \
#              -d "$(python3 -c "print('x'*11000000)")" → 413
# DTO max:     -d '{"query":"'"$(python3 -c "print('x'*9000)")"'"}' → 400
# Rate limit:  快速发4次 execute → 第4次 429
# Sec headers: curl -I https://host/ → 含 Content-Security-Policy
```

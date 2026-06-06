# RAG 知识库构建与配置管理 — 设计规格

**日期**：2026-06-06
**分支**：chore/frontend-standards-and-spa-fix
**状态**：已批准，待实施

---

## 1. 背景与目标

项目已有 RAG 基础设施（`KnowledgeIngest`、`RAGService`、`RAGHandler`），但：

- workspace 无持久化（CreateWorkspace 不写 DB，ListWorkspaces 从 Milvus 推断）
- 无 workspace 级配置（embedding model、chunk 参数、query mode）
- LLM gateway 仅支持 OpenAI / Anthropic / Ollama，需替换为国内主流提供商
- 前端无任何知识库管理页面

目标：实现租户管理员可管理 RAG 知识库（workspace），配置模型参数，上传文档，测试查询。

---

## 2. 权限模型

| 操作 | 所需角色 |
|------|---------|
| 创建 / 编辑 / 删除 workspace | tenant admin 或 owner |
| 上传文档 | tenant admin 或 owner |
| 查询 / 查看列表和统计 | tenant member（所有已登录成员）|

路由层通过 `middleware.RequireTenantRole("admin")` 或 `"owner"` 校验，已有基础设施支持。

---

## 3. 数据库层

### 变更 `pkg/tenantdb/tenant_schema.sql`

`rag_workspaces` 表追加至租户 schema DDL 文件。`ProvisionTenantSchema`（`pkg/tenantdb/schema.go`）在创建租户时自动执行该文件，`CREATE TABLE IF NOT EXISTS` 幂等安全——新租户自动获得此表，已有租户重新 provision 时不报错。

**不需要**新增 migration 文件（`internal/migration/sql/` 只管理 public schema）。

```sql
-- 追加到 tenant_schema.sql
CREATE TABLE IF NOT EXISTS rag_workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Config JSONB 结构

```json
{
  "embedding_model": "text-embedding-v3",
  "chunk_size": 512,
  "chunk_overlap": 64,
  "query_mode": "hybrid",
  "top_k": 5
}
```

**约束**：

- `embedding_model` 创建后只读（改变 embedding 维度会导致 Milvus collection 不一致）
- 允许值：`"text-embedding-v3"`（Qwen，1536 维）、`"embedding-3"`（Zhipu，2048 维）
- `query_mode` 允许值：`"vector"` / `"graph"` / `"hybrid"`

---

## 4. LLM Gateway 重构

### 删除

- `internal/llmgateway/openai.go`
- `internal/llmgateway/anthropic.go`
- `internal/llmgateway/ollama.go`

### 新增

**`internal/llmgateway/qwen.go`**

- Provider：通义千问（阿里云）
- Base URL：`https://dashscope.aliyuncs.com/compatible-mode/v1`
- 认证：`Authorization: Bearer <QwenAPIKey>`
- API 格式：兼容 OpenAI Chat Completions + Embeddings

**`internal/llmgateway/zhipu.go`**

- Provider：智谱 AI
- Base URL：`https://open.bigmodel.cn/api/paas/v4`
- 认证：`Authorization: Bearer <ZhipuAPIKey>`
- API 格式：兼容 OpenAI Chat Completions + Embeddings

### Gateway 路由规则（`gateway.go`）

model name 前缀决定 provider：

| Model Name | Provider |
|------------|----------|
| `text-embedding-v3`、`qwen-*` | Qwen |
| `embedding-3`、`glm-*` | Zhipu |

### Config 变更（`internal/config/config.go`）

移除 `OpenAIAPIKey`，新增：

```go
QwenAPIKey  string  // env: QWEN_API_KEY
ZhipuAPIKey string  // env: ZHIPU_API_KEY
```

---

## 5. 后端 Handler 与路由

### RAGHandler 结构变更

新增 `db *pgxpool.Pool` 依赖：

```go
type RAGHandler struct {
    ingestSvc  *knowledge.KnowledgeIngest
    ragService *knowledge.RAGService
    db         *pgxpool.Pool
    logger     *zap.Logger
}
```

### Workspace CRUD 方法

| 方法 | 行为 |
|------|------|
| `CreateWorkspace` | 校验 name 唯一 + config 合法 → 写入 `rag_workspaces` → 返回 201 |
| `ListWorkspaces` | 从 PG `rag_workspaces` 读取，不查 Milvus |
| `GetWorkspaceStats` | PG 读 config + Milvus 查 collection 向量数量 |
| `UpdateWorkspace` | 更新 description / config（不允许改 name 和 embedding_model）|
| `DeleteWorkspace` | 先删 Milvus collection，再删 PG 记录；任一失败记 error log |
| `UploadDocument` | 从 PG 读 workspace config → 取 embedding_model 传给 ingestSvc |

### 路由注册（`api/router.go`）

全部加 JWT + InjectTenantContext middleware；写操作加 `RequireTenantRole("admin")`：

```
POST   /knowledge/workspaces               CreateWorkspace     (admin/owner)
GET    /knowledge/workspaces               ListWorkspaces      (member)
GET    /knowledge/workspaces/:name/stats   GetWorkspaceStats   (member)
PATCH  /knowledge/workspaces/:name         UpdateWorkspace     (admin/owner)
DELETE /knowledge/workspaces/:name         DeleteWorkspace     (admin/owner)
POST   /knowledge/ingest                   UploadDocument      (admin/owner)
POST   /knowledge/query                    Query               (member)
```

原有 `/knowledge/ingest` 和 `/knowledge/query` 补上认证 middleware（当前无 JWT 校验）。

---

## 6. 前端

### 新增页面

**`web/src/pages/KnowledgePage.jsx`**（路由：`/knowledge`）

- Ant Design `Table` 展示 workspace 列表（name、description、embedding model、向量数）
- admin/owner 可见"新建知识库"按钮，点击弹 `Modal` + `Form`
- Form 字段：name（必填）、description、embedding_model（Select）、chunk_size、chunk_overlap、query_mode、top_k
- 点击行跳转 `/knowledge/:name`

**`web/src/pages/KnowledgeDetailPage.jsx`**（路由：`/knowledge/:name`）

- **配置面板**：展示并允许 admin/owner 编辑 description + config（embedding_model 只读展示）
- **文档上传**：Ant Design `Upload` 组件，仅 admin/owner 可见，上传成功后刷新 stats
- **查询测试**：文本输入框 + mode 选择器 + 提交，展示 answer 和 sources 列表

### api.js 新增

```js
export const knowledgeWorkspaces = {
  list: () => api.get('/knowledge/workspaces'),
  create: (data) => api.post('/knowledge/workspaces', data),
  stats: (name) => api.get(`/knowledge/workspaces/${name}/stats`),
  update: (name, data) => api.patch(`/knowledge/workspaces/${name}`, data),
  delete: (name) => api.delete(`/knowledge/workspaces/${name}`),
};

export const knowledge = {
  ingest: (formData) => api.post('/knowledge/ingest', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }),
  query: (data) => api.post('/knowledge/query', data),
};
```

### App.jsx 路由

```jsx
<Route path="/knowledge" element={<PrivateRoute><KnowledgePage /></PrivateRoute>} />
<Route path="/knowledge/:name" element={<PrivateRoute><KnowledgeDetailPage /></PrivateRoute>} />
```

---

## 7. 错误处理

| 场景 | 状态码 | 响应 |
|------|--------|------|
| workspace name 重复 | 409 | `{"error": "workspace already exists"}` |
| embedding_model 不在允许列表 | 400 | `{"error": "unsupported embedding model"}` |
| 尝试修改 embedding_model | 400 | `{"error": "embedding_model is immutable after creation"}` |
| 尝试修改 workspace name | 400 | `{"error": "workspace name is immutable"}` |
| Milvus / Neo4j 不可用 | 500 | `{"error": "<具体错误信息>"}` |
| DeleteWorkspace Milvus 失败 | 500 | 记 error log，不静默吞掉 |

---

## 8. 测试覆盖

- `api/handler/rag_handler_test.go`：补充 workspace CRUD httptest 用例（mock DB via `pgxmock`）
- `internal/llmgateway/qwen_test.go`：mock HTTP server，验证 model routing + embedding 调用
- `internal/llmgateway/zhipu_test.go`：同上
- 前端手动验证：创建 workspace → 上传文档 → 查询 → 编辑配置 → 删除

---

## 9. 变更文件清单

### 新增

- `internal/llmgateway/qwen.go`
- `internal/llmgateway/zhipu.go`
- `internal/llmgateway/qwen_test.go`
- `internal/llmgateway/zhipu_test.go`
- `web/src/pages/KnowledgePage.jsx`
- `web/src/pages/KnowledgeDetailPage.jsx`

### 修改

- `pkg/tenantdb/tenant_schema.sql`（追加 rag_workspaces 表）
- `internal/llmgateway/gateway.go`（路由逻辑）
- `internal/llmgateway/config.go`（API key 字段）
- `internal/config/config.go`（env 变量）
- `api/handler/rag_handler.go`（加 db 依赖，CRUD 持久化）
- `api/router.go`（注册新路由，加认证 middleware）
- `web/src/services/api.js`（新增 API 函数）
- `web/src/App.jsx`（注册新路由）

### 删除

- `internal/llmgateway/openai.go`
- `internal/llmgateway/anthropic.go`
- `internal/llmgateway/ollama.go`

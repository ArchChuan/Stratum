# Tenant-Level Embed Model Migration Plan

> **For agentic workers:** Use `superpowers:executing-plans` to implement this plan task-by-task.

**Goal:** Move embedding model selection from per-agent configuration to tenant-level set-once setting, inherited by all agents at creation time.

**Architecture:** Tenant `settings` JSONB gains an `embed_model` key (no DDL migration needed). `PATCH /tenant/embed-model` enforces set-once semantics. `buildEmbedResolver` reads `settings["embed_model"]` before the gateway cache check. Agent creation inherits from tenant settings.

**Tech Stack:** Go 1.22 · Gin · pgx v5 · React 18 · Ant Design 5

---

## Task 1: Schema backfill — agents.embed_model column

**Files:**

- Modify: `internal/migration/sql/tenant_schema.sql`
- Modify: `pkg/tenantdb/tenant_schema.sql`

**Step 1: Add idempotent backfill to migration baseline**

`internal/migration/sql/tenant_schema.sql` — append after existing `max_context_tokens` backfill (line 176):

```sql
-- idempotent backfill: embed_model for tenant-level inheritance
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';
```

**Step 2: `pkg/tenantdb/tenant_schema.sql` already has this line** (`ALTER TABLE agents ADD COLUMN IF NOT EXISTS embed_model TEXT NOT NULL DEFAULT '';`). Verify and skip if present.

**Step 3: Run**

```bash
grep -n "embed_model" internal/migration/sql/tenant_schema.sql pkg/tenantdb/tenant_schema.sql
```

Expected: both files contain the `embed_model` backfill line.

- [ ] **Step 4: Commit**

```bash
git add internal/migration/sql/tenant_schema.sql
git commit -m "fix(schema): add embed_model idempotent backfill to migration tenant_schema"
```

---

## Task 2: Backend — SetEmbedModel handler

**Files:**

- Modify: `api/handler/tenant_handler.go`

**Step 1: Write failing test** — `api/handler/tenant_handler_test.go` (if it exists; otherwise skip and verify via integration).

**Step 2: Add handler** — append to `api/handler/tenant_handler.go` after `UpdateSettings`:

```go
// SetEmbedModel PATCH /tenant/embed-model — set-once, admin/owner only
func (h *TenantHandler) SetEmbedModel(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
  return
 }

 roleVal, _ := c.Get("auth.role")
 roleStr, _ := roleVal.(string)
 if roleStr != "admin" && roleStr != "owner" {
  c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "admin or owner role required"})
  return
 }

 var req struct {
  EmbedModel string `json:"embed_model" binding:"required"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
  return
 }

 var existingJSON []byte
 _ = h.db.QueryRow(c.Request.Context(),
  "SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
 ).Scan(&existingJSON)

 existing := map[string]interface{}{}
 if len(existingJSON) > 0 {
  _ = json.Unmarshal(existingJSON, &existing)
 }

 if v, ok := existing["embed_model"]; ok && v != "" {
  c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: "embed_model already set and cannot be changed"})
  return
 }

 existing["embed_model"] = req.EmbedModel
 merged, err := json.Marshal(existing)
 if err != nil {
  c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "marshal failed"})
  return
 }

 tag, err := h.db.Exec(c.Request.Context(),
  "UPDATE public.tenants SET settings=$1, updated_at=now() WHERE id=$2 AND deleted_at IS NULL",
  merged, tenantID,
 )
 if err != nil {
  h.logger.Error("set embed_model failed", zap.Error(err))
  c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
  return
 }
 if tag.RowsAffected() == 0 {
  c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
  return
 }

 c.JSON(http.StatusOK, gin.H{"embed_model": req.EmbedModel})
}
```

**Step 3: Verify build**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/handler/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add api/handler/tenant_handler.go
git commit -m "feat(tenant): add SetEmbedModel handler with set-once semantics"
```

---

## Task 3: Router — register PATCH /tenant/embed-model

**Files:**

- Modify: `api/router.go`

**Step 1: Add route** — after `tenantGroup.PATCH("/settings", requireActive, tenantHandler.UpdateSettings)` (line 129):

```go
tenantGroup.PATCH("/embed-model", requireActive, tenantHandler.SetEmbedModel)
```

**Step 2: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/...
```

- [ ] **Step 3: Commit**

```bash
git add api/router.go
git commit -m "feat(router): register PATCH /tenant/embed-model"
```

---

## Task 4: buildEmbedResolver — read settings before cache check

**Files:**

- Modify: `api/router.go`

**Problem:** Current cache-hit path (line 333-335) returns early with `gw.DefaultEmbeddingModel()`, never consulting `settings["embed_model"]`.

**Step 1: Replace `buildEmbedResolver`** — locate the function starting around line 329 and replace its full body:

```go
func buildEmbedResolver(db *pgxpool.Pool, cache *llmgateway.TenantGatewayCache, aesKey [32]byte, logger *zap.Logger) pipeline.EmbedServiceResolver {
 return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
  // Always read settings first so embed_model is available for both paths
  var settingsJSON []byte
  if err := db.QueryRow(ctx,
   "SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
   tenantID,
  ).Scan(&settingsJSON); err != nil {
   return nil
  }
  var settings map[string]interface{}
  if err := json.Unmarshal(settingsJSON, &settings); err != nil {
   return nil
  }
  embedModel, _ := settings["embed_model"].(string)

  // Fast path: gateway already cached
  if gw, _, ok := cache.Get(tenantID); ok && gw.HasEmbeddingClient() {
   m := embedModel
   if m == "" {
    m = gw.DefaultEmbeddingModel()
   }
   return embedding.NewEmbeddingServiceWithModel(gw, m, logger)
  }

  // Slow path: build gateway from decrypted keys
  apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
  if !ok || len(apiKeysRaw) == 0 {
   return nil
  }
  decrypted := make(map[string]string, len(apiKeysRaw))
  for provider, enc := range apiKeysRaw {
   encStr, ok := enc.(string)
   if !ok || encStr == "" {
    continue
   }
   plain, err := pkgcrypto.Decrypt(aesKey, encStr)
   if err != nil {
    continue
   }
   decrypted[provider] = plain
  }
  if len(decrypted) == 0 {
   return nil
  }

  gw := llmgateway.NewGateway().WithLogger(logger)
  if qwenKey, ok := decrypted["qwen"]; ok {
   qwenClient := llmgateway.NewQwenClient(qwenKey, logger)
   gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
   gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
  }
  if zhipuKey, ok := decrypted["zhipu"]; ok {
   zhipuClient := llmgateway.NewZhipuClient(zhipuKey, logger)
   gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
   gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
  }
  for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
   if _, ok := decrypted[string(pref)]; ok {
    gw.SetDefault(pref)
    break
   }
  }
  cache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)

  if embedModel == "" {
   embedModel = gw.DefaultEmbeddingModel()
  }
  return embedding.NewEmbeddingServiceWithModel(gw, embedModel, logger)
 }
}
```

**Step 2: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/...
```

- [ ] **Step 3: Commit**

```bash
git add api/router.go
git commit -m "fix(router): buildEmbedResolver reads tenant embed_model before cache check"
```

---

## Task 5: agent_handler.go — remove EmbedModel binding:"required"

**Files:**

- Modify: `api/handler/agent_handler.go`

**Step 1:** In `CreateAgentRequest`, change:

```go
EmbedModel string `json:"embedModel" binding:"required"`
```

to:

```go
EmbedModel string `json:"embedModel"`
```

**Step 2: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/handler/...
```

- [ ] **Step 3: Commit**

```bash
git add api/handler/agent_handler.go
git commit -m "fix(agent): remove EmbedModel binding required — inherited from tenant"
```

---

## Task 6: agent_crud_handler.go — inherit embed_model from tenant settings

**Files:**

- Modify: `api/handler/agent_crud_handler.go`

**Step 1: Add `encoding/json` to imports.** The current imports are `errors fmt net/http strconv time` + internal packages. Add `"encoding/json"` to the stdlib group.

**Step 2: Replace `EmbedModel: req.EmbedModel` in `CreateAgent`.**

Replace the block in `CreateAgent` that builds `cfg`:

```go
cfg := &agent.AgentConfig{
    ID:                    id,
    Name:                  req.Name,
    Type:                  agentType,
    Description:           req.Description,
    Persona:               req.Persona,
    SystemPrompt:          req.SystemPrompt,
    LLMModel:              req.LLMModel,
    EmbedModel:            req.EmbedModel,
```

with:

```go
// Read embed_model from tenant settings (set-once at tenant level)
var settingsJSON []byte
_ = h.db.QueryRow(c.Request.Context(),
    "SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
    tenantID,
).Scan(&settingsJSON)
var tenantSettings map[string]interface{}
if len(settingsJSON) > 0 {
    _ = json.Unmarshal(settingsJSON, &tenantSettings)
}
embedModel, _ := tenantSettings["embed_model"].(string)

cfg := &agent.AgentConfig{
    ID:                    id,
    Name:                  req.Name,
    Type:                  agentType,
    Description:           req.Description,
    Persona:               req.Persona,
    SystemPrompt:          req.SystemPrompt,
    LLMModel:              req.LLMModel,
    EmbedModel:            embedModel,
```

**Note:** `tenantID` is already extracted near line 83 as the first thing `CreateAgent` does — the variable is in scope.

**Step 3: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/handler/... && go test -short ./api/...
```

- [ ] **Step 4: Commit**

```bash
git add api/handler/agent_crud_handler.go
git commit -m "feat(agent): inherit embed_model from tenant settings at create time"
```

---

## Task 7: Frontend API — setTenantEmbedModel

**Files:**

- Modify: `web/src/services/tenant.js`

**Step 1: Append one line** at the end of `tenant.js`:

```js
export const setTenantEmbedModel = (embedModel) => api.patch('/tenant/embed-model', { embed_model: embedModel });
```

**Step 2: Verify** the new export is picked up via `services/api.js` (which does `export * from './tenant'`):

```bash
cd /home/yang/go-projects/stratum/web && npm run lint 2>&1 | head -20
```

- [ ] **Step 3: Commit**

```bash
git add web/src/services/tenant.js
git commit -m "feat(frontend): add setTenantEmbedModel API function"
```

---

## Task 8: SettingsPage.jsx — add embed model section (set-once UI)

**Files:**

- Modify: `web/src/pages/tenant/SettingsPage.jsx`

**Step 1: Update imports** — add `Select, Tag` to antd import, add `DatabaseOutlined` to icons import, add `setTenantEmbedModel, getAvailableModels` to services import:

```js
import { Form, Input, Button, Typography, message, Space, Divider, Spin, Select, Tag } from 'antd';
import { EyeInvisibleOutlined, EyeTwoTone, CheckCircleFilled, SettingOutlined, KeyOutlined, DatabaseOutlined } from '@ant-design/icons';
import { getTenantSettings, updateTenant, setTenantEmbedModel, getAvailableModels } from '../../services/api';
```

**Step 2: Add state** — after `const [maskedKeys, setMaskedKeys] = useState({});` add:

```js
const [embedForm] = Form.useForm();
const [embedModel, setEmbedModel] = useState('');
const [embeddingModels, setEmbeddingModels] = useState([]);
const [embedModelsLoading, setEmbedModelsLoading] = useState(false);
const [embedLoading, setEmbedLoading] = useState(false);
```

**Step 3: Update `loadSettings`** — after `setMaskedKeys(apiKeys);` add:

```js
setEmbedModel(res.data?.settings?.embed_model || '');
```

**Step 4: Add useEffect for embedding model list** — after the `useEffect(() => { loadSettings(); }, [loadSettings]);` line:

```js
useEffect(() => {
  let cancelled = false;
  setEmbedModelsLoading(true);
  getAvailableModels()
    .then(res => {
      if (!cancelled) {
        const embeds = res.data?.embedding_models;
        setEmbeddingModels(embeds?.length > 0 ? embeds : ['text-embedding-v3', 'text-embedding-v2', 'embedding-3']);
      }
    })
    .catch(() => { if (!cancelled) setEmbeddingModels(['text-embedding-v3', 'text-embedding-v2', 'embedding-3']); })
    .finally(() => { if (!cancelled) setEmbedModelsLoading(false); });
  return () => { cancelled = true; };
}, []); // eslint-disable-line react-hooks/exhaustive-deps
```

**Step 5: Add `handleEmbedSave`** — after `handleKeySave`:

```js
const handleEmbedSave = async (values) => {
  setEmbedLoading(true);
  try {
    await setTenantEmbedModel(values.embedModel);
    setEmbedModel(values.embedModel);
    embedForm.resetFields();
    message.success('嵌入模型已设置');
  } catch (err) {
    if (err.response?.status !== 403)
      message.error(err.response?.data?.message || err.response?.data?.error || '设置失败');
  } finally {
    setEmbedLoading(false);
  }
};
```

**Step 6: Add JSX section** — after the closing `</div>` of the API Key section (after line 162):

```jsx
{/* 嵌入模型 */}
<div style={{
  background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0',
  padding: 24, maxWidth: 560, marginTop: 16,
}}>
  <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 0 }}>
    <SectionHeader icon={<DatabaseOutlined />} title="嵌入模型" subtitle="所有 Agent 共享的向量嵌入模型，设置后不可更改" />
    {!canEditKeys && <Text type="secondary" style={{ fontSize: 12 }}>仅 owner / admin 可编辑</Text>}
  </div>
  {embedModel ? (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <Tag color="blue" style={{ fontSize: 13, padding: '2px 10px' }}>{embedModel}</Tag>
      <Text type="secondary" style={{ fontSize: 12 }}>已配置，不可更改</Text>
    </div>
  ) : (
    <Form form={embedForm} layout="inline" onFinish={handleEmbedSave}>
      <Form.Item name="embedModel" rules={[{ required: true, message: '请选择嵌入模型' }]}>
        <Select
          placeholder="选择嵌入模型"
          style={{ width: 260 }}
          loading={embedModelsLoading}
          disabled={!canEditKeys}
        >
          {embeddingModels.map(m => <Select.Option key={m} value={m}>{m}</Select.Option>)}
        </Select>
      </Form.Item>
      <Form.Item>
        <Button type="primary" htmlType="submit" loading={embedLoading} disabled={!canEditKeys}>
          设置
        </Button>
      </Form.Item>
    </Form>
  )}
</div>
```

**Step 7: Verify**

```bash
cd /home/yang/go-projects/stratum/web && npm run lint 2>&1 | head -30
```

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/tenant/SettingsPage.jsx
git commit -m "feat(settings): add tenant-level embed model set-once configuration section"
```

---

## Task 9: useCreateAgentPage.js — remove embeddingModels logic

**Files:**

- Modify: `web/src/hooks/useCreateAgentPage.js`

**Step 1: Remove `FALLBACK_EMBEDDING_MODELS` constant** (line 7).

**Step 2: Remove `embeddingModels` state** — remove `const [embeddingModels, setEmbeddingModels] = useState([]);` (line 16).

**Step 3: In the `useEffect`, remove all embed-related lines:**

- Remove `const embeds = modelsRes.value.data.embedding_models?.length > 0 ? ... : FALLBACK_EMBEDDING_MODELS;`
- Remove `setEmbeddingModels(embeds);`
- Remove `form.setFieldValue('embedModel', embeds[0]);`
- In the `else` branch, remove `setEmbeddingModels(FALLBACK_EMBEDDING_MODELS);` and `form.setFieldValue('embedModel', FALLBACK_EMBEDDING_MODELS[0]);`

**Step 4: Update return object** — remove `embeddingModels` from the return:

```js
return {
  form, loading, skills, mcpServers, workspaces,
  availableModels, modelsLoading,
  navigate, onFinish,
};
```

**Step 5: Verify**

```bash
cd /home/yang/go-projects/stratum/web && npm run lint 2>&1 | head -30
```

- [ ] **Step 6: Commit**

```bash
git add web/src/hooks/useCreateAgentPage.js
git commit -m "refactor(hooks): remove embeddingModels from useCreateAgentPage"
```

---

## Task 10: CreateAgentPage.jsx — remove embedModel Form.Item

**Files:**

- Modify: `web/src/pages/CreateAgentPage.jsx`

**Step 1: Update destructuring** — remove `embeddingModels` from the `useCreateAgentPage()` call (line 41-43 area):

```js
const {
  form, loading, skills, mcpServers, workspaces,
  availableModels, modelsLoading,
  navigate, onFinish,
} = useCreateAgentPage();
```

**Step 2: Delete the embedModel `Form.Item` block** (lines 107-112):

```jsx
<Form.Item label="嵌入模型" name="embedModel" rules={[{ required: true, message: '请选择嵌入模型' }]}
  tooltip="记忆系统向量化使用的 Embedding 模型，需与 LLM 同一提供商">
  <Select placeholder="选择嵌入模型" loading={modelsLoading}>
    {embeddingModels.map(m => <Option key={m} value={m}>{m}</Option>)}
  </Select>
</Form.Item>
```

**Step 3: Verify**

```bash
cd /home/yang/go-projects/stratum/web && npm run lint 2>&1 | head -30
```

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/CreateAgentPage.jsx
git commit -m "refactor(ui): remove embed model field from CreateAgentPage"
```

---

## Task 11: useEditAgentPage.js — remove embeddingModels state

**Files:**

- Modify: `web/src/hooks/useEditAgentPage.js`

**Step 1: Remove `FALLBACK_EMBEDDING_MODELS` constant** (line 8).

**Step 2: Remove `embeddingModels` state** (line 19): `const [embeddingModels, setEmbeddingModels] = useState([]);`

**Step 3: In `useEffect`, remove embed-related lines:**

- Remove `const embeds = modelsRes.value.data.embedding_models?.length > 0 ? ... : FALLBACK_EMBEDDING_MODELS;`
- Remove `setEmbeddingModels(embeds);`
- In the `else` branch, remove `setEmbeddingModels(FALLBACK_EMBEDDING_MODELS);`

**Step 4: In `form.setFieldsValue`, remove the `embedModel` line** (line 68):

```js
form.setFieldsValue({
  name: a.name,
  description: a.description,
  type: a.type || 'react',
  persona: a.persona,
  systemPrompt: a.systemPrompt,
  llmModel: a.llmModel,
  // embedModel: a.embedModel,  ← remove this line
  maxIterations: a.maxIterations,
  maxContextTokens: a.maxContextTokens || 8000,
  allowedSkills: a.allowedSkills || [],
  mcpServerIds: a.mcpServerIds || [],
  knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
});
```

**Step 5: Update return object** — remove `embeddingModels`:

```js
return {
  id, form, loading, pageLoading,
  skills, mcpServers, workspaces,
  availableModels, modelsLoading,
  navigate, onFinish,
};
```

**Step 6: Verify**

```bash
cd /home/yang/go-projects/stratum/web && npm run lint 2>&1 | head -30
```

- [ ] **Step 7: Commit**

```bash
git add web/src/hooks/useEditAgentPage.js
git commit -m "refactor(hooks): remove embeddingModels from useEditAgentPage"
```

---

## Task 12: EditAgentPage.jsx — remove embedModel Form.Item

**Files:**

- Modify: `web/src/pages/EditAgentPage.jsx`

**Step 1: Update destructuring** — remove `embeddingModels` from the `useEditAgentPage()` call (line 40-44):

```js
const {
  form, loading, pageLoading,
  skills, mcpServers, workspaces,
  availableModels, modelsLoading,
  navigate, onFinish,
} = useEditAgentPage();
```

**Step 2: Delete the embedModel `Form.Item` block** (lines 112-117):

```jsx
<Form.Item label="嵌入模型" name="embedModel" rules={[{ required: true, message: '请选择嵌入模型' }]}
  tooltip="创建后不可更改，修改需重新向量化所有历史记忆">
  <Select placeholder="选择嵌入模型" loading={modelsLoading} disabled>
    {embeddingModels.map(m => <Option key={m} value={m}>{m}</Option>)}
  </Select>
</Form.Item>
```

**Step 3: Verify full build**

```bash
cd /home/yang/go-projects/stratum && go vet ./... && go test -short ./...
cd /home/yang/go-projects/stratum/web && npm run lint && npm run build 2>&1 | tail -20
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/EditAgentPage.jsx
git commit -m "refactor(ui): remove embed model field from EditAgentPage"
```

---

## Execution Order

Tasks 1-6 are independent of 7-12 (backend vs frontend). Run in parallel or sequentially:

```
1 → 2 → 3 → 4  (schema + handler + route + resolver, must be in order)
5 → 6           (agent handler cleanup, after 4 is done)
7 → 8           (frontend API + settings page)
9 → 10          (create agent cleanup)
11 → 12         (edit agent cleanup)
```

## Final Verification

```bash
# Backend
cd /home/yang/go-projects/stratum
go vet ./... && go test -short ./...

# Frontend
cd web && npm run lint && npm run build

# Manual smoke test (requires running server)
# 1. PATCH /tenant/embed-model {"embed_model":"text-embedding-v3"} → 200
# 2. PATCH /tenant/embed-model {"embed_model":"other"} → 400 (already set)
# 3. POST /agent {"name":"test","llmModel":"glm-4","maxIterations":10} → 201, embed_model inherited
# 4. SettingsPage shows embed model Tag after setting
```

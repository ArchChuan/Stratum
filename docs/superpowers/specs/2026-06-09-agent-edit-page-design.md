# Agent Edit Page Design

**Date**: 2026-06-09
**Branch**: feat/tenant-suspend-enforcement
**Status**: Approved

## Overview

Add an agent edit page (`/agents/:id/edit`) that lets users modify all fields set during agent creation. Also fixes an existing bug where `allowed_skills` is never persisted to the database.

## Scope

- New migration: add `allowed_skills TEXT[]` column to `agents` table
- Backend: `Registry.Update` method + `PUT /agents/:id` endpoint
- Backend bugfix: `Registry.Register`, `Get`, `GetAll` all wire `allowed_skills`
- Frontend: `EditAgentPage.jsx` page + route
- Frontend: "编辑" button in `AgentsListPage.jsx` operations column

## Database Layer

### Migration

File: `internal/migration/sql/005_add_agent_allowed_skills.up.sql`

```sql
ALTER TABLE agents ADD COLUMN IF NOT EXISTS allowed_skills TEXT[] NOT NULL DEFAULT '{}';
```

Also update `pkg/tenantdb/tenant_schema.sql` to include the column for new tenants.

### AgentConfig

Add field to `internal/agent/agent.go`:

```go
type AgentConfig struct {
    ID            string
    Name          string
    Type          AgentType
    Description   string
    Persona       string
    SystemPrompt  string
    LLMModel      string
    MaxIterations int
    AllowedSkills []string  // NEW
    Capabilities  []AgentCapability
}
```

## Backend

### Registry changes (`internal/agent/registry.go`)

**Register** — add `allowed_skills` as 9th parameter:

```sql
INSERT INTO agents (id, name, type, description, persona, system_prompt, llm_model, max_iterations, allowed_skills)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
```

**Get / GetAll** — extend SELECT and Scan to include `allowed_skills`.

**Update** — new method:

```go
func (r *Registry) Update(ctx context.Context, a Agent) error
```

SQL:

```sql
UPDATE agents
SET name=$2, type=$3, description=$4, persona=$5, system_prompt=$6,
    llm_model=$7, max_iterations=$8, allowed_skills=$9, updated_at=NOW()
WHERE id=$1
```

Returns `fmt.Errorf("agent not found: %s", id)` if `RowsAffected() == 0`.

### Handler (`api/handler/agent_handler.go`)

New method `UpdateAgent`:

- Validates tenant context (same guard as other handlers)
- Binds JSON to `CreateAgentRequest` (reuse same struct)
- Calls `registry.Get` to confirm agent exists → 404 if not
- Builds new `AgentConfig` with same type-switch logic as `CreateAgent`
- Calls `registry.Update`
- Returns updated `AgentResponse` with HTTP 200

### Router (`api/router.go`)

```go
agents.PUT("/:id", requireActive, agentHandler.UpdateAgent)
```

### Frontend API (`web/src/services/api.js`)

```js
export const updateAgent = (id, data) => api.put(`/agents/${id}`, data);
```

## Frontend

### EditAgentPage.jsx (`web/src/pages/EditAgentPage.jsx`)

Mirrors `CreateAgentPage.jsx` exactly, with these differences:

| Aspect | CreateAgentPage | EditAgentPage |
|--------|----------------|---------------|
| Page title | 创建智能代理 | 编辑智能代理 |
| Submit button | 创建代理 | 保存修改 |
| API call | `createAgent(values)` | `updateAgent(id, values)` |
| On mount | load models + skills | load models + skills + `getAgentById(id)` → `form.setFieldsValue` |
| Success notification | 创建成功 | 保存成功 |
| Route param | none | `id` from `useParams()` |

Form fields: name (required), description, persona, systemPrompt, llmModel (required), maxIterations (required, 1–20), allowedSkills — identical to CreateAgentPage.

Error handling: if `getAgentById` fails on mount, show `message.error` and `navigate('/agents')`.

### AgentsListPage.jsx

Add "编辑" button to operations column before "执行":

```jsx
import { EditOutlined } from '@ant-design/icons';

<Button
  type="link"
  icon={<EditOutlined />}
  onClick={() => navigate(`/agents/${record.id}/edit`)}
>
  编辑
</Button>
```

Requires `useNavigate` (already imported via React Router).

### Route registration

In `web/src/App.jsx` (or the router file), add:

```jsx
import EditAgentPage from './pages/EditAgentPage';

<Route path="/agents/:id/edit" element={<EditAgentPage />} />
```

## Data Flow

```
User clicks 编辑
  → navigate /agents/:id/edit
  → EditAgentPage mounts
  → getAgentById(id) → GET /agents/:id
  → form.setFieldsValue(data)
User edits fields → Submit
  → updateAgent(id, values) → PUT /agents/:id
  → Registry.Update → UPDATE agents SET ... WHERE id=$1
  → 200 AgentResponse → navigate('/agents')
```

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Agent not found on mount | `message.error` + redirect to `/agents` |
| PUT returns 404 | `notification.error` with server message |
| PUT returns 400 | `notification.error` with validation message |
| PUT returns 403 | silently ignored (tenant suspended middleware) |

## Testing

- `Registry.Update` unit test: success + not-found cases
- `UpdateAgent` handler test: 200 success, 404 not-found, 400 bad request
- Manual: edit an agent, verify all fields persist after page reload

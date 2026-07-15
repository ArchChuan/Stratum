# Skill Capability Package Design

> **Historical design (implemented).** The live model and API contract are defined by `internal/skill/`, `api/http/handler/skill_handler.go`, and the tenant schema. Migration phases below are retained for rationale.

## Decision

Stratum Skill is a minimal capability package for Agents, not a `prompt/code/http/llm` runtime wrapper.

The core runtime split is:

```text
Skill             = product object and long-lived capability asset
Tool Contract     = Agent-visible callable protocol generated from a published Skill version
Implementation    = execution snapshot behind the contract
```

Publishing a Skill freezes:

- capability snapshot
- tool contract snapshot
- implementation snapshot
- test baseline snapshot

Agents only see published tool contracts. Draft Skills are editable and testable, but they do not affect normal Agent execution.

## Current State

The current backend has a tenant-scoped `skills` table:

```text
skills(id, name, description, type, config, created_at, updated_at)
```

The current Agent loop injects Skill tools in `AgentService.buildExtraTools`:

```text
allowedSkills []skillID
→ LookupSkill returns name/description
→ toolName = tenant_{tenantID}_{skill_name}
→ input schema is always { prompt: string }
→ SkillToolIndex maps toolName → skillID
```

The ReAct tool node then does:

```text
LLM tool call name
→ SkillToolIndex lookup
→ CapabilityGateway CapSkill
→ SkillAdapter
→ skillgateway.DefaultGateway
→ DBSkillAdapter
→ SELECT skills WHERE id=$1
→ execute type/config implementation
```

This means the Agent currently sees a generic prompt-shaped tool, not a real Skill contract. The proposed design changes the Agent-facing boundary from `skill name + prompt schema` to a published `ToolContract`.

## Product Model

### Skill

Long-lived product object.

```text
id
name
description
status              draft | published | archived
active_version_id
draft_version_id
created_by
created_at
updated_at
```

`skills` does not own implementation type. It owns identity and lifecycle.

### Skill Version

Snapshot of a draft or published Skill.

```text
id
skill_id
version_no          null for draft, monotonically increasing for published versions
status              draft | published | deprecated
capability          JSONB
tool_contract       JSONB
implementation      JSONB
test_baseline       JSONB
publish_checks      JSONB
created_by
created_at
updated_at
published_at
```

Only one draft version exists per Skill.

### Capability

User-facing business definition.

```json
{
  "goal": "判断客户投诉类型并给出处理建议",
  "whenToUse": "用户表达投诉、退款、物流延迟、商品质量问题时",
  "inputSpec": "客户投诉原文，可包含用户等级和订单上下文",
  "outputSpec": "投诉分类、分类理由、建议处理动作",
  "examples": [
    {
      "input": "我的快递三天没更新",
      "expectedOutput": "物流问题，建议查询物流并安抚用户"
    }
  ]
}
```

Creation relies on examples and natural language. Publishing relies on confirmed contract and passing tests.

### Tool Contract

Agent-visible callable protocol.

```json
{
  "toolName": "classify_complaint",
  "description": "当用户表达投诉、退款、物流延迟、商品质量问题时，判断投诉类型并给出处理建议。",
  "inputSchema": {
    "type": "object",
    "properties": {
      "complaintText": {
        "type": "string",
        "description": "客户投诉原文"
      }
    },
    "required": ["complaintText"]
  },
  "outputSchema": {
    "type": "object",
    "properties": {
      "category": {"type": "string"},
      "reason": {"type": "string"},
      "action": {"type": "string"}
    }
  },
  "callingGuidance": "只在需要结构化判断投诉类别时调用；普通闲聊不要调用。",
  "confirmed": true
}
```

The system generates the first contract from capability. Users can micro-adjust it in a contract preview. Published contracts are immutable; changes create a new draft/version.

### Implementation

Internal execution snapshot.

```json
{
  "mode": "prompt",
  "source": {
    "promptTemplate": "..."
  },
  "runtime": {
    "model": "gpt-4.1-mini",
    "temperature": 0.2,
    "maxTokens": 2048,
    "timeoutSec": 30
  },
  "permissions": {
    "riskLevel": "low",
    "humanApproval": "never"
  },
  "secretRefs": []
}
```

Implementation mode can be `prompt`, `code`, `http`, `mcp`, or later `generated`. It is not a user-facing Skill type.

### Test Cases

Skill-level long-lived assets.

```text
skill_test_cases
- id
- skill_id
- name
- input JSONB
- expected_output JSONB
- assertion_mode exact | contains | schema | llm_judge
- enabled
- created_at
- updated_at
```

Publishing stores the then-current enabled test cases and run result in `skill_versions.test_baseline`.

### Eval Runs

Every draft test, Agent trial, and publish check records an eval run.

```text
skill_eval_runs
- id
- skill_id
- version_id nullable
- test_case_id nullable
- mode single_skill | agent_trial | publish_check
- input JSONB
- actual_output JSONB
- expected_output JSONB
- passed
- trace JSONB
- duration_ms
- created_at
```

## UI Flow

### Skills List

Show long-lived Skill assets, not implementation types.

Columns should stay sparse:

- name
- status
- active version
- draft state
- last test result

### Create Skill

Creation is intentionally small:

- name
- goal: what capability does this Skill provide?
- whenToUse: when should Agent call it?
- sample input
- expected output

The system creates:

- `skills` product row
- initial draft `skill_versions` row
- inferred `capability.inputSpec` and `capability.outputSpec`
- generated `tool_contract`
- default `implementation`
- first `skill_test_case`

### Skill Workspace

After creation, users maintain the Skill in a workspace with tabs:

1. Capability
   - goal
   - whenToUse
   - input/output spec
   - examples
   - save draft
   - regenerate contract

2. Contract
   - toolName
   - description
   - inputSchema
   - outputSchema
   - callingGuidance
   - validation status
   - confirm contract

3. Implementation
   - mode: prompt/code/http/mcp
   - source editor
   - runtime settings
   - permissions, secret refs, sandbox policy
   - implementation-only test

4. Tests
   - test case list
   - single Skill test
   - Agent trial
   - eval run history
   - publish check result

5. Versions
   - active published version
   - historical published versions
   - deprecate
   - rollback

## API Design

Initial target API:

```text
POST   /skills                  create product object + initial draft version
GET    /skills
GET    /skills/:id

PATCH  /skills/:id/draft/capability
POST   /skills/:id/draft/contract/generate
PATCH  /skills/:id/draft/contract
PATCH  /skills/:id/draft/implementation

POST   /skills/:id/test-cases
PATCH  /skills/:id/test-cases/:caseID
DELETE /skills/:id/test-cases/:caseID

POST   /skills/:id/draft/test
POST   /skills/:id/draft/agent-trial
POST   /skills/:id/publish

GET    /skills/:id/versions
POST   /skills/:id/rollback
```

Legacy compatibility can keep existing endpoints during migration:

```text
PUT  /skills/:id                legacy update shape, mapped to draft implementation where possible
POST /skills/:id/test           legacy saved skill test, mapped to active or draft test endpoint
POST /skills/test-draft         legacy request-shaped draft test, mapped to temporary draft execution
```

But new UI should target draft/version APIs.

## Agent Loop Collaboration

### Current Problem

`buildExtraTools` currently builds every Skill tool with:

```text
toolName = tenant_{tenantID}_{skill_name}
description = name + ": " + description
schema = { prompt: string }
index = toolName → skillID
```

This hides the Skill contract from the LLM and makes every Skill look like a generic text processor.

### Target Behavior

Agent execution should resolve allowed Skills to published Skill versions before entering the ReAct loop:

```text
AgentConfig.AllowedSkills
→ SkillToolResolver.Resolve(tenantID, allowedSkillIDs)
→ []ToolDefinition from published tool_contract snapshots
→ SkillToolIndex maps toolName → {skillID, versionID}
→ ReAct LLM node receives real ToolDefinition schemas
→ ReAct tool node routes versioned skill execution
```

`port.ToolDefinition` should come directly from `skill_versions.tool_contract`:

```text
Name        = toolContract.toolName, tenant-scoped or canonicalized to avoid collisions
Description = toolContract.description + callingGuidance
InputSchema = toolContract.inputSchema
```

### Tool Name Strategy

The model-facing tool name must be stable and schema-safe. Prefer:

```text
tenant_{tenantID}_{toolName}
```

where `toolName` is generated from contract and validated for allowed characters. The contract also stores the raw logical name.

### Version Resolution

Initial policy:

```text
Agent allowed skill ID → latest active published version
```

Execution trace must persist the resolved `skill_id` and `version_id`.

Later extension:

```text
agent_skill_bindings(agent_id, skill_id, version_policy, version_id, enabled)
```

### ReAct Tool Node

The ReAct graph should remain hard-coded control logic. It should not ask AI to route execution. The only change is the index value:

```text
current: toolName → skillID
target:  toolName → SkillToolRef{SkillID, VersionID}
```

The capability request should carry version:

```text
SkillCapRequest{
  SkillID,
  VersionID,
  Input: tc.Arguments,
}
```

### Skill Gateway

`DBSkillAdapter` should execute `skill_versions.implementation`, not mutable `skills.type/config`.

Execution path:

```text
SkillAdapter
→ skillgateway.DefaultGateway
→ DBSkillAdapter
→ SELECT skill_versions WHERE id=$versionID AND status='published'
→ build executor from implementation snapshot
→ execute with contract-shaped input
```

Draft tests should use draft version directly and bypass Agent availability.

### Agent Trial

Agent trial is a test harness, not a new production control path.

Input:

```text
skill_id
draft_version_id
user_task
optional agent_id
```

Behavior:

```text
build a temporary ToolDefinition from draft tool_contract
inject only that tool plus selected existing tools into a bounded ReAct run
observe whether the model calls the draft tool
record selected tool name, arguments, output, final answer, and pass/fail notes
```

The draft tool must not be added to `AgentConfig.AllowedSkills` and must not become visible to normal Agent execution.

## Publish Gate

Publishing requires:

1. Capability complete
   - name
   - goal
   - whenToUse
   - at least one example/test case

2. Contract confirmed
   - toolName valid and unique in tenant
   - inputSchema valid JSON Schema
   - outputSchema valid JSON Schema
   - description specific enough for tool selection
   - `confirmed = true`

3. Implementation valid
   - implementation mode present
   - runtime values within constants
   - secrets/permissions valid for mode
   - code implementation passes static analyzer

4. Tests pass
   - at least one enabled test case passes
   - publish check records test baseline
   - Agent trial recommended but optional in phase 1

## Migration Plan

### Phase 1: Add Versioned Model Beside Legacy Table

Add tenant DDL:

```text
skills.active_version_id
skills.draft_version_id
skill_versions
skill_test_cases
skill_eval_runs
```

Keep existing `skills.type/config` columns for compatibility.

Backfill each legacy Skill:

```text
legacy skills row
→ skill_versions v1 published
→ capability inferred from name/description/type/config
→ tool_contract generated with legacy prompt-shaped schema if no better schema exists
→ implementation from type/config
→ active_version_id = v1
```

### Phase 2: New Skill Service APIs

Introduce new application objects:

- `Capability`
- `ToolContract`
- `Implementation`
- `SkillVersion`
- `TestCase`
- `EvalRun`

Add draft/version APIs while keeping legacy CRUD.

### Phase 3: Agent Resolver Switch

Replace `SkillLookup.LookupSkill` usage with a version-aware resolver:

```go
type SkillToolResolver interface {
    ResolveTools(ctx context.Context, tenantID string, skillIDs []string) ([]ToolDefinition, map[string]SkillToolRef, error)
}
```

`AgentService.buildExtraTools` should merge MCP tools and resolved Skill tools.

### Phase 4: Gateway Version Execution

Extend `SkillCapRequest` with `VersionID`. Make `DBSkillAdapter` prefer version execution when version is present, falling back to legacy `skills.type/config` only for compatibility.

### Phase 5: Frontend Workspace

Replace create/edit runtime forms with:

- minimal create page
- Skill workspace tabs
- contract preview
- implementation editor
- tests and publish panel
- versions tab

## Open Questions

1. Whether generated implementation should be created synchronously on `POST /skills` or lazily when opening Implementation tab.
2. Whether Agent trial is optional or required for publish after phase 1.
3. Whether toolName uniqueness should be tenant-wide or per-Agent. Recommendation: tenant-wide for simpler routing and observability.
4. Whether published implementation secrets are stored as `secretRefs` only. Recommendation: yes, no raw secrets in tenant JSONB.

## Non-Goals

- Do not make Skill a mini workflow engine.
- Do not let AI decide routing/retry/state machine behavior.
- Do not expose `prompt/code/http/llm` as Skill creation types.
- Do not mutate published contract or implementation in place.

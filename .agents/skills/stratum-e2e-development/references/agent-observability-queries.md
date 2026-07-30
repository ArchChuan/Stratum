# Agent 可观测性验证查询

验证 agent trace 基座时，优先使用已有可工作的 tenant/user/agent，避免把新建 agent 的 wiring 问题误判成 trace 问题。

## 查找最近 execution

```bash
docker exec -i stratum-postgres-1 psql -U stratum -d stratum -P pager=off <<'SQL'
SELECT id, trace_id, agent_id, status, total_tokens, duration_ms, created_at
FROM "tenant_<tenant_id>".agent_executions
ORDER BY created_at DESC
LIMIT 5;
SQL
```

真实 API 调用应让 prompt 明确触发工具，例如"请回答你是谁，并调用可用工具回忆我是谁"。响应至少检查：

- HTTP status 为 200
- `steps > 0`
- `tokensUsed > 0`
- `toolCalls` 包含预期工具名

## Trace 事件质量聚合

```sql
WITH latest AS (
  SELECT id::text AS execution_id, trace_id
  FROM "tenant_<tenant_id>".agent_executions
  ORDER BY created_at DESC
  LIMIT 1
)
SELECT 'event_quality' AS section,
       count(*) AS events,
       count(*) FILTER (WHERE execution_id <> (SELECT execution_id FROM latest)) AS execution_id_mismatch,
       count(*) FILTER (WHERE execution_id = '') AS missing_execution_id,
       count(*) FILTER (WHERE sequence_no = 0) AS zero_sequence,
       count(*) FILTER (WHERE provider_type = '') AS missing_provider,
       count(*) FILTER (WHERE observation_type IN ('llm','tool') AND node_id = '') AS missing_node,
       count(*) FILTER (WHERE started_at IS NULL) AS missing_started,
       count(*) FILTER (WHERE ended_at IS NULL) AS missing_ended,
       count(*) FILTER (WHERE event_type='tool.call_finished' AND tool_trace_id = '') AS missing_tool_link,
       min(sequence_no) AS min_seq,
       max(sequence_no) AS max_seq
FROM "tenant_<tenant_id>".agent_trace_events
WHERE trace_id=(SELECT trace_id FROM latest);
```

## 工具 raw IO 和 summary 质量

```sql
WITH latest AS (
  SELECT id::text AS execution_id, trace_id
  FROM "tenant_<tenant_id>".agent_executions
  ORDER BY created_at DESC
  LIMIT 1
)
SELECT 'tool_quality' AS section,
       count(*) AS tool_traces,
       count(*) FILTER (WHERE execution_id <> (SELECT execution_id FROM latest)) AS execution_id_mismatch,
       count(*) FILTER (WHERE execution_id = '') AS missing_execution_id,
       count(*) FILTER (WHERE arguments_json = '{}'::jsonb) AS missing_args,
       count(*) FILTER (WHERE raw_result_json = '{}'::jsonb AND raw_result_text = '') AS missing_raw_result,
       count(*) FILTER (WHERE summary = '') AS missing_summary,
       bool_or(raw_truncated) AS any_raw_truncated
FROM "tenant_<tenant_id>".agent_tool_traces
WHERE trace_id=(SELECT trace_id FROM latest);
```

## 下一轮上下文摘要检查

```sql
WITH latest AS (
  SELECT trace_id
  FROM "tenant_<tenant_id>".agent_executions
  ORDER BY created_at DESC
  LIMIT 1
), conv AS (
  SELECT conversation_id
  FROM "tenant_<tenant_id>".agent_trace_events
  WHERE trace_id=(SELECT trace_id FROM latest)
    AND conversation_id IS NOT NULL
  LIMIT 1
)
SELECT role, left(content, 180) AS content, created_at
FROM "tenant_<tenant_id>".chat_messages
WHERE conversation_id=(SELECT conversation_id FROM conv)
ORDER BY created_at DESC
LIMIT 3;
```

## 完成标准

- `agent_executions.status = success`，且 `total_tokens`、`duration_ms` 大于 0
- `agent_trace_events` 至少包含 LLM request/response、tool started/finished、final answer
- `agent_tool_traces` 有 arguments、raw result、summary
- `chat_messages` 有"本轮工具观察摘要"，用于下一轮上下文
- `execution_id` 与 `trace_id` 在 execution、trace events、tool traces 三处对齐
- 日志中没有 `failed to save trace events`、`failed to save tool traces`、`unused argument`、`column does not exist`

## 常见诊断

| 现象 | 根因方向 |
|------|----------|
| `CapGateway not set` | Agent wiring 或 TenantResolver 注入问题 |
| `provider not found` | 租户 LLM key 或 provider 注册问题，不是工具解析问题 |
| `unknown tool` | 工具名和 SkillToolIndex / MCP 映射问题 |
| `toolCalls` 为空 | 工具可能暴露了，但 prompt、description 或 query 不足以触发模型调用 |
| 数据库列不存在 | tenant schema provision 未完成或 backfill 顺序错误 |

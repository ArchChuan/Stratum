---
name: stratum-e2e-development
description: >-
  在 Stratum 项目功能开发完成后使用：验证代码在真实系统中是否跑通，发现问题时定位根因并修复，直到端到端闭环。不用于需求分析或方案设计阶段。
---

# Stratum E2E 验证与 Bug 修复

**使用时机**：功能代码已写完，需要验证它在真实服务中是否正确工作；或收到 bug 报告，需要复现、定位、修复、再验证。

核心目标：不是"代码写完"，而是"用户目标在真实系统里跑通"。

## 基本原则

- 验证驱动：先确认验收标准，再启动服务，再执行验证。
- 不只依赖单测；按影响范围做真实端到端验证。
- 前端改动必须模拟真实用户操作，不能只看页面能打开。
- 后端改动必须启动服务并请求真实 API，不能只看 handler 单测。
- 数据库相关改动必须验证写入、读取、约束、tenant schema、backfill 或迁移顺序。
- Agent/Skill/MCP/Memory/Knowledge/IAM 能力改动必须验证执行链路，不只验证配置保存。
- 验证失败时继续定位并修复，直到需求目标闭环。
- 不打印 token、refresh token、API key、password、原始密钥或敏感配置。
- 可以读取本地 `.env` 和测试数据库完成验证，但不得泄露敏感值。
- 可以创建 `tmp-` 前缀临时脚本做验证；完成前必须删除。
- 自己启动的后端、前端或辅助进程，完成前必须停止，或明确说明仍在运行和原因。

## 工作流程

### 1. 确认验收标准

接手前先明确：

- 本次功能/修复的目标操作是什么。
- 成功时系统应该呈现什么状态（API 响应、页面状态、数据库写入、日志证据）。
- 涉及哪些层：前端、HTTP API、application、DB、tenant schema、Agent 链路。

写成可验证句子，例如：

```text
POST /agents/:id/execute/stream 返回 steps>0，日志中出现 react.tool.response 且 error 为空，知识库内容在 content_preview 中可见。
```

### 2. 选择验证层级

按改动影响面选择验证，不做无关大范围验证。

- 纯函数或模型转换：跑单元测试、边界输入、构建或类型检查。
- 后端 API：跑 Go 测试，启动后端，请求真实 HTTP API，验证错误响应和读回结果。
- 前端页面：跑前端测试、lint、build，启动前端，用 Playwright 或等价工具模拟点击、输入、保存、刷新。
- 数据库或 tenant schema：验证新租户 provision、旧租户 backfill、表列索引约束、写入读取、DDL 幂等。
- Agent/工具链路：验证配置保存、工具暴露、LLM tool call、工具路由、结果回传、execution/trace 记录。

### 3. 后端验证

需要真实服务时启动后端：

```bash
set -a; . ./.env; set +a; go run ./cmd/server
```

健康检查：

```bash
curl -fsS http://localhost:8080/health
```

后端常规测试：

```bash
go test ./... -count=1
```

如果全量耗时过高，先跑相关包；最终报告必须说明未跑全量的原因。

真实 API 验证必须检查：

- HTTP 状态码。
- 响应体关键字段。
- 错误响应是否符合预期。
- 写入后是否能 GET 读回。
- 数据库状态是否真的变化。

#### JWT Self-Mint（无需用户登录的后端 API 验证）

需要 Bearer token 时，从 `.env` 读取 `JWT_PRIVATE_KEY_PEM` 自签即可，无需走登录流程。

**Go 临时脚本模板**（`tmp-e2e-xxx.go`，build tag `ignore`）：

```go
//go:build ignore

package main

import (
 "crypto/x509"
 "encoding/pem"
 "fmt"
 "os"
 "strings"
 "time"

 "github.com/golang-jwt/jwt/v5"
)

func main() {
 // 从 .env 读取私钥（set -a; . ./.env; set +a 已注入到环境）
 raw := os.Getenv("JWT_PRIVATE_KEY_PEM")
 raw = strings.ReplaceAll(raw, `\n`, "\n")
 block, _ := pem.Decode([]byte(raw))
 key, _ := x509.ParsePKCS1PrivateKey(block.Bytes)

 now := time.Now()
 token, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
  "sub":         "<user_id>",
  "tid":         "<tenant_id>",
  "role":        "member",
  "global_role": "user",
  "system_role": "user",
  "jti":         fmt.Sprintf("e2e-%d", now.UnixNano()),
  "iat":         now.Unix(),
  "exp":         now.Add(30 * time.Minute).Unix(),
 }).SignedString(key)

 // 使用 token 调用 API（不打印 token 本身）
 // req, _ := http.NewRequest("POST", "http://localhost:8080/...", body)
 // req.Header.Set("Authorization", "Bearer "+token)
 _ = token
}
```

关键：

- claims 字段名必须与 `internal/iam/application/jwt_service.go` 中的 `jwtAccessClaims` 一致（`tid`/`role`/`global_role`/`system_role`）
- 私钥格式：`x509.ParsePKCS1PrivateKey`（PKCS#1 RSA）
- `.env` 中换行符可能是字面 `\n`，需 `strings.ReplaceAll` 转义
- 不得打印 token、私钥或任何原始凭据

**Python + curl 快速模板**（适合一次性验证，不落盘）：

```bash
python3 - <<'PY'
import json
import re
import subprocess
import sys
import time
import uuid
from pathlib import Path

import jwt

env_text = Path(".env").read_text()
# 下面的正则仅用于从本地 .env 提取私钥；本身不含任何密钥。
key = re.search(
    r'JWT_PRIVATE_KEY_PEM="(-----BEGIN RSA PRIVATE KEY-----.*?-----END RSA PRIVATE KEY-----)"',  # gitleaks:allow
    env_text,
    re.S,
).group(1)

tenant_id = "<tenant_id>"
user_id = "<user_id>"
agent_id = "<agent_id>"
now = int(time.time())
token = jwt.encode({
    "tid": tenant_id,
    "sub": user_id,
    "role": "owner",
    "global_role": "global_admin",
    "system_role": "admin",
    "iat": now,
    "exp": now + 3600,
    "jti": str(uuid.uuid4())[:8],
}, key, algorithm="RS256")

payload = {
    "query": "请完成一次端到端验证，并在需要时调用可用工具。",
    "options": {"maxSteps": 4, "timeout": 120},
}

res = subprocess.run([
    "curl", "-sS", "-w", "\nHTTP_STATUS:%{http_code}\n",
    "-X", "POST", f"http://localhost:8080/agents/{agent_id}/execute",
    "-H", f"Authorization: Bearer {token}",
    "-H", "Content-Type: application/json",
    "--data", json.dumps(payload, ensure_ascii=False),
], text=True, capture_output=True, timeout=180)

print(res.stdout)
if res.stderr:
    print(res.stderr, file=sys.stderr)
PY
```

关键：

- 这个模板只打印 API 响应和 HTTP status，不打印 token。
- 如果本机没有 `PyJWT`，优先改用 Go 临时脚本，或在当前虚拟环境里安装测试依赖后再跑。
- claims 的角色值按场景调整；需要覆盖管理员 API 时使用 owner/admin 类 claims，普通用户 API 用 member/user。

### 4. 前端验证

涉及 UI 时启动或复用前端服务：

```bash
npm --prefix web run dev
```

前端基础检查：

```bash
npm --prefix web run lint -- --quiet
npm --prefix web run build
```

有相关测试时运行对应测试：

```bash
npm --prefix web test -- <相关测试文件>
```

#### WSL2 前端验证（接口推断为主）

WSL2 headless 截图有字体缺失问题（中文乱码），**不使用截图**。改用以下两种方式：

**方式一：Windows 浏览器直访（交互验证）**

WSL2 端口自动转发到 Windows 宿主机，在 Windows Chrome/Edge 打开 `http://localhost:<port>`，DevTools 完全可用。适合人工确认 UI 状态。

**方式二：Playwright DOM 断言 + 网络拦截（自动化验证）**

不截图，改用 `page.evaluate()` 提取文本、`page.waitForResponse()` 捕获 API 响应，从接口返回值推断 UI 状态。

临时 spec 模板（`web/e2e/tmp-xxx.spec.ts`，完成后删除）：

```ts
import { test, expect } from '@playwright/test';

const BASE = 'http://localhost:3004'; // 以 Vite 实际输出端口为准

test('登录并验证目标页面数据', async ({ page }) => {
  // 1. 拦截目标 API
  const apiPromise = page.waitForResponse(
    res => res.url().includes('/api/v1/') && res.status() === 200
  );

  // 2. 登录
  await page.goto(`${BASE}/login`);
  await page.fill('input[type="email"]', process.env.E2E_EMAIL!);
  await page.fill('input[type="password"]', process.env.E2E_PASSWORD!);
  await page.click('button[type="submit"]');
  await page.waitForURL(`${BASE}/**`);

  // 3. 导航到目标页面
  await page.goto(`${BASE}/target-path`);
  const res = await apiPromise;
  const body = await res.json();

  // 4. 断言接口数据（不截图，从响应推断 UI 状态）
  expect(body.data).toBeDefined();
  expect(res.status()).toBe(200);

  // 5. DOM 文本提取（可选，不依赖字体）
  const text = await page.evaluate(() =>
    document.querySelector('[data-testid="main-content"]')?.textContent ?? ''
  );
  expect(text.length).toBeGreaterThan(0);
});
```

运行：

```bash
cd web && E2E_EMAIL=xxx E2E_PASSWORD=xxx npx playwright test e2e/tmp-xxx.spec.ts --reporter=list
```

**方式三：纯 API 验证（最快，跳过浏览器）**

前端改动涉及数据展示逻辑时，直接用 curl + JWT 验证后端接口返回值，从接口数据推断前端应渲染的内容是否正确，无需启动浏览器。详见第 3 节 JWT Self-Mint 模板。

浏览器验证必须覆盖真实用户路径：

- 打开目标页面。
- 登录或注入有效认证态。
- 点击、输入、选择、保存、删除或执行。
- 等待真实请求完成。
- 验证成功/失败提示。
- 刷新页面后确认数据仍正确。

### 5. 数据库验证

数据库相关功能必须验证：

- 目标表、列、索引、约束存在。
- INSERT / UPDATE / SELECT 与业务契约一致。
- 多租户 `search_path` 或 `tenantdb` 路由正确。
- 新租户 schema 能 provision。
- 已有租户 schema 能 backfill。
- `ADD CONSTRAINT`、`CREATE INDEX`、`ALTER TABLE ADD COLUMN` 都幂等。
- PostgreSQL `timestamptz` 显示 `2026-07-09 08:24:34+00` 是 UTC 时间，正常；北京时间需要加 8 小时。
- WSL 本地没有 `psql` 时，优先通过 Postgres 容器执行查询：

```bash
docker exec -i stratum-postgres-1 psql -U stratum -d stratum -P pager=off <<'SQL'
SELECT now();
SQL
```

Tenant schema 特别注意：

- tenant-only DDL 放 `pkg/storage/postgres/tenant_schema.sql`。
- `CREATE TABLE` 新增列后紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`。
- 依赖新列的索引、约束、查询必须排在 backfill 之后。
- 后续 backfill 可能因重复 constraint 中断；基础业务必需列应尽量放在建表或早期 ALTER。
- Stratum 常通过 PgBouncer 连接 Postgres；tenant schema provisioning 必须在事务里使用 `SET LOCAL search_path`，避免 transaction pooling 下普通 `SET search_path` 丢失或污染连接。
- 启动日志出现 `failed to provision tenant schema` 时，不要只验证当前租户；要确认所有历史租户是否能 provision，避免旧租户缺列导致后续 trace/knowledge/memory 链路随机失败。

### 6. Agent / Tool 链路验证

涉及 Agent、Skill、MCP、Memory、Knowledge 时，不能只验证配置保存。

必须按目标验证执行链路：

- 配置能创建和读回。
- Agent allowedSkills / MCP tools / 知识库 / 记忆配置能保存。
- 执行时工具能暴露给 LLM。
- LLM 能产生预期 tool call。
- tool call 能路由到正确后端能力。
- 工具结果能回到下一轮 LLM。
- 最终 response 正常返回。

关键证据：

- `error` 为空。
- `steps > 0`。
- `toolCalls` 包含预期工具名。
- trace / execution record / 日志中有关键步骤。

#### Agent Observability E2E 验证

验证 agent trace 基座时，优先使用已有可工作的 tenant/user/agent，避免把新建 agent 的 wiring 问题误判成 trace 问题。可先从数据库找最近成功 execution：

```bash
docker exec -i stratum-postgres-1 psql -U stratum -d stratum -P pager=off <<'SQL'
SELECT id, trace_id, agent_id, status, total_tokens, duration_ms, created_at
FROM "tenant_<tenant_id>".agent_executions
ORDER BY created_at DESC
LIMIT 5;
SQL
```

真实 API 调用应让 prompt 明确触发工具，例如“请回答你是谁，并调用可用工具回忆我是谁”。响应至少检查：

- HTTP status 为 200。
- `steps > 0`。
- `tokensUsed > 0`。
- `toolCalls` 包含预期工具名。

随后查最新 trace 的质量聚合：

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

工具 raw IO 和 summary 质量：

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

下一轮上下文摘要检查：

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

完成标准：

- `agent_executions.status = success`，且 `total_tokens`、`duration_ms` 大于 0。
- `agent_trace_events` 至少包含 LLM request/response、tool started/finished、final answer。
- `agent_tool_traces` 有 arguments、raw result、summary。
- `chat_messages` 有“本轮工具观察摘要”，用于下一轮上下文。
- `execution_id` 与 `trace_id` 在 execution、trace events、tool traces 三处对齐。
- 日志中没有 `failed to save trace events`、`failed to save tool traces`、`unused argument`、`column does not exist`。

常见判断：

- `CapGateway not set`：Agent wiring 或 TenantResolver 注入问题。
- `provider not found`：租户 LLM key 或 provider 注册问题，不是工具解析问题。
- `unknown tool`：工具名和 SkillToolIndex / MCP 映射问题。
- `toolCalls` 为空：工具可能暴露了，但 prompt、description 或 query 不足以触发模型调用。
- 数据库列不存在：tenant schema provision 未完成或 backfill 顺序错误。

### 7. 失败后循环修复

验证失败时：

1. 定位失败层：前端、HTTP、application、agent loop、外部模型、数据库、tenant schema、权限认证。
2. 收集证据：响应体、请求 payload、后端日志、数据库状态、调用链。
3. 找到最小根因，不猜测。
4. 修改代码或配置。
5. 重跑失败场景。
6. 通过后再跑相关回归测试。

不能因为单测通过就忽略真实 E2E 失败。

## 内嵌 Skill 调用

执行流程中可以随时调用其他 skill：

| 阶段 | 推荐 skill |
|------|-----------|
| 定位失败根因 | `superpowers:systematic-debugging` |
| 补充或改写测试 | `agent-skills:test` |
| 安全相关改动 | `agent-skills:security-and-hardening` |
| 代码质量扫描 | `code-review` 或 `simplify` |
| 提交并开 PR | `commit-commands:commit-push-pr` |

规则：

- 不强制每阶段都调，按实际需要触发。
- skill 返回后继续本 skill 的工作流程，不中断 E2E 闭环目标。

## 临时脚本规则

可以创建临时脚本验证：

- 临时 Go API E2E。
- 临时 Playwright spec。
- 临时 SQL 检查脚本。

要求：

- 文件名用 `tmp-` 前缀。
- 不提交临时脚本。
- 完成前删除。
- 不写入或打印密钥。
- 输出只包含状态码、ID、错误摘要、验证结果。

## 最终完成标准

最终回复前确认：

- 相关测试已通过。
- 后端服务已真实验证，或说明为什么不涉及。
- 前端功能已真实操作，或说明为什么不涉及。
- 数据库链路已验证，或说明为什么不涉及。
- 临时文件已删除。
- 自启动进程已停止，或明确说明仍在运行和原因。
- 未验证项、残余风险、无关环境问题必须明确暴露。

推荐最终报告格式：

```text
已完成端到端验证。

改动结果：
- 完成了 xxx
- 修复了 xxx

验证结果：
- 后端测试：通过，命令 xxx
- 前端测试：通过，命令 xxx
- API E2E：通过，验证了 xxx
- 浏览器 E2E：通过，验证了 xxx
- 数据库链路：通过，验证了 xxx

清理情况：
- 临时脚本已删除
- 后端进程已停止 / 或服务仍运行在 xxx

剩余风险：
- xxx 不阻塞本次目标
```

如果没有完成完整 E2E，必须明确说：

```text
没有完成完整 E2E，原因是 xxx。
已完成的验证是 xxx。
未验证的风险是 xxx。
```

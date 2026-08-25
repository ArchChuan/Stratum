# CLAUDE.md 大清洗设计

日期：2026-08-25
状态：已获用户批准（方案 A：全量系统评估 + 两阶段 + 平衡清洗 + 端到端测试重构）

## 背景

`docs/agent/instructions.md` 是 `CLAUDE.md` / `AGENTS.md` 的单一事实源，由
`scripts/quality/generate-agent-instructions.sh` 拼装 prefix 模板生成。但文档体系与实际系统已明显脱节，
存在过时事实、缺失项和幽灵引用，需要按系统现状全面评估并清洗。

## 现状评估发现（探索阶段已核实）

| 项 | 文档声称 | 系统实际 |
|----|---------|---------|
| bounded context | 11 个 | **14 个**（缺 `audit`、`collab`、`parameters`） |
| OTEL | v1.40.0 | **v1.42.0**（go.mod） |
| React Router | 6.26 | **7.18.2**（web/package.json） |
| pkg/ 包清单 | 缺多个 | 多出 `dag`、`jsonschema`、`messaging`、`platformknowledge`、`safetext`、`timeutil`、`tokenutil`、`postgres`、`storage/{filestore,objectstore}` |
| `config/prod.yaml` | 被引用（"禁止修改"） | **不存在**（config/ 只有 config.go、nacos.go、pgbouncer.ini、userlist.txt） |
| middleware | 文档列 9 个 | 多出 `body_limit`、`rate_limit`、`public_error`、`require_default_tenant`、`system_role_check` |
| web modules | 部分 | 多出 `approvals`、`audit`、`collab`、`operation-gate`、`parameters`、`scheduled-task` |
| 依赖清单 | 缺多个 | 多出 minio、unidoc、modelcontextprotocol/go-sdk、robfig/cron、bufbuild/protocompile 等 |

文档体系结构：`docs/agent/instructions.md`（共同主体）→ `generate-agent-instructions.sh` → `CLAUDE.md` + `AGENTS.md`；
被引用的 16 个 doc 位于 "Layered context index"；另有 6 个未引用的 doc + templates/。

## 决策

1. **范围**：全量系统评估 —— instructions.md + 全部 25 个 docs/agent/ 文件（16 被引用 + 6 未引用 + templates/）
2. **流程**：两阶段 —— 第一阶段产出评估报告，用户确认后才进入第二阶段修改
3. **清洗深度**：平衡清洗（方案 A）—— 修正事实 + 补充缺失 + 精简冗余，未引用文档按价值保留/归档/合并
4. **端到端测试重构**：提升 e2e 地位 + 测试门槛原则 + 专用测试子 agent

## 第一阶段：全量评估

### 评估对象

- `docs/agent/instructions.md`（主体）与 `docs/agent/templates/{claude,agents}-prefix.md`
- 16 个被引用的 doc：project / architecture / backend-go / constants / migration-tenant / api / agent / agent-chat-flow / milvus / nats / memory-facts / frontend / product / observability / deployment-architecture / knowledge-workspace
- 6 个未引用的 doc：bug-lessons / e2e-standards / memory-trajectory-reflection / review-platform-config-versions / tiered-memory / verification-ci-authority

### 评估基准（系统现状事实源，按优先级）

1. `go.mod` / `go.sum`、`web/package.json` —— 依赖与版本
2. `internal/`、`pkg/`、`api/`、`web/src/` 实际目录与文件 —— 结构描述
3. 关键文件存在性（`.go-arch-lint.yml`、`config/prod.yaml`、`api/http/contract_test.go` 等）
4. `Makefile` targets、`.github/workflows/`、`.pre-commit-config.yaml` —— 流程描述
5. 文档内引用的具体文件路径 / API 签名是否仍存在

### 执行方式

- 子代理按角色分组并行评估（每组一个 agent 对照系统现状验证 2-3 个文档），主 agent 汇总去重、交叉核对成完整评估报告
- 关键交叉检查：CLAUDE.md 引用的 16 个 doc 是否都存在、内容是否与其在索引里的定位相符

### 评估报告格式（每文档）

```
文档名 | 最后修改 | 角色(被引用/未引用) | 状态(✅准确/⚠️部分过时/❌过时) | 过时点清单 | 处理建议(修改/保留/归档/删除/合并)
```

报告存 `docs/superpowers/plans/2026-08-25-claude-md-cleanup-audit.md`，随 PR 提交，作为用户确认依据。

## 第二阶段：清洗执行（报告确认后）

- 按用户确认的报告逐项处理：修改过时 doc、补充缺失的 context/pkg 描述、精简重复、处理未引用文档去留
- 同步更新 `instructions.md`（context 列表、依赖清单、pkg 结构、e2e 门槛、测试 agent 机制等）
- `make agent-instructions` 重新生成 `CLAUDE.md` / `AGENTS.md`

## 端到端测试重构

### 测试门槛原则（写入 CLAUDE.md）

- **trivial 改动**（只改字段、只改一个小 bug、常量值调整）：最小验证（unit + contract）
- **非 trivial 改动**（功能、Bug 修复、联调、数据库链路、Agent/Skill/MCP/Memory/Knowledge/IAM、文档体系本身）：
  必须完整测试，对应 `make test-verify-before-pr`，按 `.test/verification.yaml` 风险级别自动选择层级
  （R2 → e2e-short，R3 → +e2e-soak，R4 → +release-soak）
- **本清洗 PR 命中 `agent-governance` R3 规则**（`docs/agent/**`、`CLAUDE.md`），必须走完整验证闭环

### 专用测试子 agent：`stratum-e2e-tester`

- 定义文件：`.claude/agents/stratum-e2e-tester.md`（frontmatter：name / description / tools：
  Bash + Playwright/Chrome DevTools 浏览器自动化 + 文件读写）
- system prompt 以 `stratum-e2e-development` skill 为操作手册：确认验收标准 → 按 verification.yaml 定风险级 →
  执行对应层级验证（后端/前端/数据库/Agent 链路）→ 产出结构化验证报告
  （用例清单、HTTP/UI/DB 证据、failed/skipped/reconciled、清理结果）
- 红线继承：不输出 token/密钥/API key、远端只读、临时脚本必须清理
- **存放位置**：`.claude/agents/`（被 .gitignore 忽略，本地不入库）；CLAUDE.md 只描述机制

### 与 agent-skills:test-engineer 的职责分工

- `agent-skills:test-engineer`（插件通用 agent）：测试**编写/设计/覆盖率分析**，不可在仓库内定制
- `stratum-e2e-tester`（新建）：**系统验收/端到端验证执行**，封装 stratum 专属验证体系
- 两者定位互补：主 agent 开发中需要写测试时用 test-engineer，开发完成需要系统验收时派发 stratum-e2e-tester
- stratum-e2e-tester 的 system prompt 骨架借鉴 test-engineer 的结构化输出风格

### 主 agent 与测试 agent 协作

开发完成 → 主 agent 派发 `stratum-e2e-tester`（传入变更范围 + 验收标准）→ 测试 agent 独立执行验证 →
返回报告 → 主 agent 依据 failed/skipped/unreconciled 决定继续修复或完成。

## 验证与交付

- `make agent-instructions-check` —— 生成物与源一致
- `npx markdownlint docs/agent/ CLAUDE.md AGENTS.md` —— 文档格式
- 手动核对关键过时点已修复：OTEL 1.42、React Router 7、14 contexts、pkg 清单、`config/prod.yaml` 引用
- R3 验证闭环：`make test-verify-before-pr`（含 e2e-short），按需 e2e-soak
- 文档改动不触碰代码，`go test` 不受影响

## Git 工作流

- `bash scripts/new-worktree.sh ../stratum-doc-cleanup feat/doc-cleanup`（已完成）
- 第一阶段（评估报告）与第二阶段（清洗）各自 commit
- PR 描述含 What / Why / HowToTest
- CI 全绿后合并，`git worktree remove ../stratum-doc-cleanup` 清理

## 成功标准

1. `docs/agent/instructions.md` 及所有被引用的 doc 与系统现状一致（无过时事实、无幽灵引用）
2. 14 个 context、新 pkg/中间件/handler、依赖版本全部正确反映
3. 未引用文档有明确处理（保留/归档/删除/合并）
4. CLAUDE.md 明确测试门槛与测试 agent 机制
5. `.claude/agents/stratum-e2e-tester.md` 可被主 agent 派发执行验证
6. `make agent-instructions-check`、markdownlint、R3 验证闭环全绿

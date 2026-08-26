<div align="center">

# Stratum

**企业级 AI 原生应用编排平台 · DDD 分层架构 · 多租户隔离**

[![Live Demo](https://img.shields.io/badge/Live%20Demo-101.200.181.141%3A8443-2ea44f?style=flat&logo=vercel&logoColor=white)](https://101.200.181.141:8443)
[![CI](https://github.com/ArchChuan/Stratum/actions/workflows/ci.yml/badge.svg)](https://github.com/ArchChuan/Stratum/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white)](web/package.json)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[🚀 在线体验](https://101.200.181.141:8443) · [📖 文档](#文档) · [⚡ 快速开始](#快速开始) · [🏛 架构总览](#架构总览) · [🤝 贡献](#贡献)

</div>

---

> **🌐 在线 Demo** — 公网入口为 **<https://101.200.181.141:8443>**，健康检查为
> **<https://101.200.181.141:8443/api/health>**。该部署使用公网 IP 和自签名 TLS，不要求域名。
> GitHub OAuth 仅在部署环境完成对应 OAuth App 配置后可用。

---

## 简介

Stratum 是一套面向私有化部署的 AI 应用底座。后端用 Go 实现，按 DDD 划分为 14 个 bounded context；前端基于 React 18 + Ant Design 5。它把 Agent 编排、Workflow 与定时调度、Skill、Evaluation 评测、记忆系统、GraphRAG 知识库、MCP 协议、协作编辑和多租户 IAM 串成统一的开发与运行链路。

## 核心特性

- **Agent 编排** — ReAct 循环，工具调用 / 流式输出 / 中断恢复 · A2A 协议
- **Workflow 与定时调度** — 持久化静态 DAG，版本发布、异步运行、审批与人工介入 · cron 定时触发
- **Skill** — instruction bundle 管理、revision 发布与候选优化、变更审计
- **记忆系统** — PostgreSQL 持久化 + 向量语义检索 + 实体提取 + JetStream 三阶段 Pipeline
- **GraphRAG 知识库** — Milvus 向量检索，文档摄取自动分块向量化，语义检索
- **MCP 协议** — Model Context Protocol 适配，服务器管理 + 工具级风险策略与审批
- **评测与进化（Evaluation）** — 统一评测 CLI（e2e-eval-check）、LLM 判题、多执行器、PR 前自动评测
- **多租户隔离** — PostgreSQL schema 级租户隔离 · GitHub OAuth · JWT RS256
- **协作编辑** — 多人共同编辑，白名单管控
- **可观测性** — Zap 结构化日志 · OpenTelemetry · Prometheus 指标
- **生命周期管理** — Harness 统一注册、有序启停、健康检查

## 架构总览

```
┌─────────────────────────────────────────────────────────────────┐
│  api/http      handler · dto · middleware · router             │
│  api/wiring    Container（组合根） · Resolver（跨租户解析）     │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 依赖单向 ↓
┌─────────────────────────────────────────────────────────────────┐
│  internal/<ctx>/  domain → application → infrastructure         │
│                                                                  │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐    │
│  │ agent      │ │ memory     │ │ knowledge  │ │ skill      │    │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘    │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐    │
│  │ workflow   │ │ scheduler  │ │ evaluation │ │ collab     │    │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘    │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐    │
│  │ mcp        │ │ iam        │ │ llmgateway │ │ platform   │    │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘    │
│  ┌────────────┐ ┌────────────┐                                  │
│  │ audit      │ │ parameters │                                  │
│  └────────────┘ └────────────┘                                  │
└──────────────────────────┬──────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────────┐
│  pkg/  storage · messaging · observability · crypto · ...      │
│        无业务依赖，单向被 internal/ 引用                        │
└─────────────────────────────────────────────────────────────────┘
```

**分层规则**：

- `handler → application → domain/port`，`infrastructure` 实现 port
- 跨 context 走消费方 `domain/port/`，禁止 import 兄弟上下文 `application` / `infrastructure`
- `pkg/` 不 import `internal/`；`domain/` 零第三方依赖
- 错误：domain 定义 `Err*` → infrastructure 翻译 → application 编排 → middleware 映射 HTTP

## 在线体验（Live Demo）

<table>
<tr>
<td width="60%">

**🌐 Demo 地址**：<https://101.200.181.141:8443>

**试用步骤**：

1. 打开 Demo 首页，点击右上角 **GitHub 登录**
2. 授权后自动创建租户，进入工作台
3. 在 **Agent** 页发起对话，或去 **知识库** 上传文档体验 GraphRAG 检索
4. 通过 **Skill** 编排原子技能，或在 **MCP** 页接入外部工具

> Demo 环境为单节点 K3s 部署，仅用于功能演示，请勿存放真实或敏感数据。生产部署请参考 [`docs/DEPLOYMENT_GUIDE.md`](docs/DEPLOYMENT_GUIDE.md)。

</td>
<td width="40%" align="center">

[![Try it Live](https://img.shields.io/badge/Try%20it%20Live-101.200.181.141%3A8443-2ea44f?style=for-the-badge)](https://101.200.181.141:8443)

**功能覆盖**

✅ Agent 对话（ReAct）
✅ GraphRAG 知识库（Milvus）
✅ Skill 编排
✅ MCP 工具集成
✅ Anthropic · Ollama · Qwen · Zhipu
✅ 多租户隔离
✅ GitHub OAuth

</td>
</tr>
</table>

## 技术栈

| 层级 | 选型 |
|------|------|
| 后端语言 | Go 1.25.12 |
| Web 框架 | Gin v1.9 |
| 数据库 | PostgreSQL（pgx v5）· Redis（go-redis v9）· MinIO（对象存储） |
| 消息总线 | NATS JetStream（nats.go v1.51） |
| 向量库 | Milvus v2.4.2 |
| 鉴权 | JWT RS256（golang-jwt v5）+ GitHub OAuth |
| 可观测性 | Zap · OpenTelemetry v1.42 · Prometheus |
| LLM | OpenAI 兼容（通义千问 Qwen · 智谱 Zhipu）· Anthropic · Ollama |
| 前端 | React 18.3 · Vite 6.4 · Ant Design 5.20 · React Router 7 · Axios · Zod · Recharts |

## 快速开始

### 前置要求

- Go 1.25+
- Node.js 22+ / npm
- Docker + Docker Compose（本地基础设施）

### 启动

```bash
# 1. 克隆并装依赖
git clone https://github.com/ArchChuan/Stratum.git
cd Stratum
cp .env.example .env          # 填入 GitHub OAuth、LLM API Key 等

# 2. 拉起本地基础设施（Postgres · Redis · NATS · Milvus）
make dev-up

# 3. 启动后端
make run                      # 默认监听 :8080

# 4. 启动前端
make fe-dev                   # 默认监听 :3000
```

完整的环境变量见 [`.env.example`](.env.example)，部署细节见 [`docs/DEPLOYMENT_GUIDE.md`](docs/DEPLOYMENT_GUIDE.md)。

## 目录结构

```
stratum/
├── api/
│   ├── http/                 HTTP 接入层（handler · dto · middleware · router）
│   └── wiring/               组合根：Container 装配 · Resolver 跨租户解析
├── internal/                 14 个 bounded context（DDD 分层）
│   ├── agent/                ReAct 编排 · ChatStore · A2A 协议
│   ├── knowledge/            GraphRAG 知识库 · 文档摄取 · Milvus 向量检索
│   ├── memory/               记忆持久化 · 实体提取 · 向量检索 · JetStream Pipeline
│   ├── skill/                instruction bundle · revision 发布 · 候选优化
│   ├── workflow/             静态 DAG · 版本发布 · 异步运行 · 审批与人工介入
│   ├── scheduler/            cron 定时触发 workflow · worker 轮询
│   ├── evaluation/           评测控制面 · suite/revision · 异步 run/job · e2e-eval-check
│   ├── collab/               协作编辑 · 白名单 · 多人共同编辑
│   ├── mcp/                  MCP 服务器管理 · ToolRegistry · 工具级风险策略
│   ├── iam/                  Tenant · Admin · OAuth · JWT · Onboard
│   ├── llmgateway/           LLM 统一网关 · ModelRegistry · 嵌入服务
│   ├── platform/             Harness 生命周期 · 告警 · E2E attestation
│   ├── audit/                资源变更审计事件
│   └── parameters/           平台级参数注册 · 解析 · 校验
├── pkg/                      无业务抽象（storage · messaging · observability · ...）
├── cmd/                      服务与工具入口（server · e2e-eval-check · migrate-public · ...）
├── proto/                    参数契约唯一事实源（protoc-gen-ginstruct 生成前后端 DTO）
├── web/                      React 前端（src/modules 按域组织）
├── docs/                     架构 · 部署 · 模块文档
├── helm/ k8s/ grafana/       部署与可观测性配置
├── .test/                    verification.yaml 验证计划 · 风险分级
└── openspec/                 OpenSpec 规范变更记录
```

详细分层职责见 [`docs/engineering-standards.md`](docs/engineering-standards.md) 与 [`docs/agent/project.md`](docs/agent/project.md)。

## 开发命令

```bash
# Backend
make be-install               # go mod download && tidy
make be-fmt                   # gofmt
make be-lint                  # golangci-lint
make be-test                  # go test -race
make be-build                 # 编译到 bin/
make run                      # 启动后端

# Frontend
make fe-install               # npm install
make fe-lint                  # eslint
make fe-typecheck             # tsc --noEmit
make fe-test                  # vitest
make fe-build                 # vite build
make fe-dev                   # vite dev server

# 契约与代码质量
make proto-gen                # 由 proto/ 重新生成前后端 DTO
make contract-test            # HTTP 合约 golden 测试
make code-quality             # 复杂度门禁（圈复杂度 ≤10 等）
make risk-guardrails          # 风险回归守卫（授权 · 凭证 · 租户边界 · 回滚）

# 验证与系统验收
make test-verify-before-pr    # PR 前完整验证（unit + contract + e2e，按风险分级升级）
make e2e-system-short / e2e-system-soak / e2e-system-release-soak
make eval-dev / eval-ci / eval-pr   # 评测：开发态 / CI / PR 前

# 基础设施
make dev-up / dev-down        # 启停 infra + observability
make obs-up / obs-down        # 仅启停 Prometheus / Grafana / Jaeger
make infra-up / infra-down    # 仅启停依赖服务（Postgres · Redis · NATS · Milvus）

# 部署
make k8s-deploy               # kubectl apply
make helm-install             # helm install
make ci-cd-full               # 全量 CI + 部署到 dev

make help                     # 完整命令清单
```

## 文档

### 架构与约定

- [`docs/documentation-map.md`](docs/documentation-map.md) — 当前文档导航与历史资料边界
- [`docs/engineering-standards.md`](docs/engineering-standards.md) — 项目铁律：DDD 分层、命名、日志、安全
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — 贡献流程、PR 规范
- [`DEVELOPMENT.md`](DEVELOPMENT.md) — 本地开发指南
- [`docs/agent/project.md`](docs/agent/project.md) — 项目事实参考（目录、依赖版本）

### 模块详解

| 模块 | 文档 |
|------|------|
| Agent / ReAct | [`docs/agent/agent.md`](docs/agent/agent.md) |
| HTTP API 契约 | [`docs/agent/api.md`](docs/agent/api.md) |
| Milvus 向量检索 | [`docs/agent/milvus.md`](docs/agent/milvus.md) |
| NATS 事件总线 | [`docs/agent/nats.md`](docs/agent/nats.md) |
| 可观测性 | [`docs/agent/observability.md`](docs/agent/observability.md) |
| MCP 集成 | [`docs/mcp-integration.md`](docs/mcp-integration.md) · [`docs/mcp-quickstart.md`](docs/mcp-quickstart.md) |
| LLM 接入 | [`docs/LLM_INTEGRATION.md`](docs/LLM_INTEGRATION.md) · [`docs/QUICKSTART_LLM.md`](docs/QUICKSTART_LLM.md) |
| 数据持久化 | [`docs/DATA_PERSISTENCE.md`](docs/DATA_PERSISTENCE.md) |
| 部署指南 | [`docs/DEPLOYMENT_GUIDE.md`](docs/DEPLOYMENT_GUIDE.md) · [`docs/STARTUP_GUIDE.md`](docs/STARTUP_GUIDE.md) |
| Agent 对话链路 | [`docs/agent/agent-chat-flow.md`](docs/agent/agent-chat-flow.md) |
| 记忆系统 | [`docs/agent/memory-facts.md`](docs/agent/memory-facts.md) |
| 后端规范 | [`docs/agent/backend-go.md`](docs/agent/backend-go.md) · [`docs/agent/constants.md`](docs/agent/constants.md) |
| 租户迁移 | [`docs/agent/migration-tenant.md`](docs/agent/migration-tenant.md) |
| 部署架构 | [`docs/agent/deployment-architecture.md`](docs/agent/deployment-architecture.md) · [`docs/agent/knowledge-workspace.md`](docs/agent/knowledge-workspace.md) |
| 前端架构 | [`web/ARCHITECTURE.md`](web/ARCHITECTURE.md) · [`web/README.md`](web/README.md) · [`docs/agent/frontend.md`](docs/agent/frontend.md) |

### 规范变更

[`openspec/`](openspec/) — 按 OpenSpec 流程管理的规范变更提案与归档。

## 路线图

- [ ] Agent 多步骤规划（Plan-and-Execute）
- [ ] 多模态 Agent（图片理解 · 语音交互）

## 贡献

欢迎 Issue 与 PR。提交前请阅读 [`CONTRIBUTING.md`](CONTRIBUTING.md) 与 [`docs/engineering-standards.md`](docs/engineering-standards.md)，确保：

1. 后端通过 `make be-fmt be-lint be-test`
2. 前端通过 `make fe-lint fe-build`
3. 质量门禁通过 `make code-quality && go vet ./...`，风险改动通过 `make risk-guardrails`
4. PR 标题格式：`[type](scope): description`（type: feat/fix/refactor/perf/test/docs/chore/ci）
5. PR 描述包含 **What · Why · HowToTest** 三段

CI 包含单元测试、lint、合约 golden 测试、质量棘轮、迁移护栏、风险回归守卫和 Stateful Browser E2E 验证，PR 合并前全部通过。功能改动还需通过 `make test-verify-before-pr` 完成系统验收。

## 协议

[Apache License 2.0](LICENSE)

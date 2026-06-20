<div align="center">

# Stratum

**企业级 AI 原生应用编排平台 · DDD 分层架构 · 多租户隔离**

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white)](web/package.json)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[文档](#文档) · [快速开始](#快速开始) · [架构总览](#架构总览) · [贡献](#贡献)

</div>

---

## 简介

Stratum 是一套面向私有化部署的 AI 应用底座。后端用 Go 实现，按 DDD 划分为 8 个 bounded context；前端基于 React 18 + Ant Design 5。它把 Agent 编排、Skill 网关、记忆系统、GraphRAG、MCP 协议、多租户 IAM 串成一条统一的开发与运行链路。

## 核心特性

- **Agent 编排** — ReAct StateGraph，工具调用 / 流式输出 / 中断恢复
- **Skill Gateway** — 原子化执行、流水线 DSL、熔断器、审计日志
- **记忆系统** — PostgreSQL 持久化 + 向量语义检索 + 实体图谱 + JetStream 三阶段 Pipeline
- **GraphRAG** — Milvus 向量库 + Neo4j 知识图谱，文档摄取自动分块向量化
- **MCP 协议** — Model Context Protocol 适配，工具与模型统一接入
- **多租户隔离** — PostgreSQL schema 级租户隔离 · GitHub OAuth · JWT RS256
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
│  ┌──────────┐ ┌──────────┐ ┌────────────┐ ┌──────────┐          │
│  │ agent    │ │ memory   │ │ knowledge  │ │ skill    │          │
│  └──────────┘ └──────────┘ └────────────┘ └──────────┘          │
│  ┌──────────┐ ┌──────────┐ ┌────────────┐ ┌──────────┐          │
│  │ mcp      │ │ iam      │ │ llmgateway │ │ platform │          │
│  └──────────┘ └──────────┘ └────────────┘ └──────────┘          │
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

## 技术栈

| 层级 | 选型 |
|------|------|
| 后端语言 | Go 1.25 |
| Web 框架 | Gin v1.9 |
| 数据库 | PostgreSQL（pgx v5）· Redis（go-redis v9） |
| 消息总线 | NATS JetStream v1.31 |
| 向量库 | Milvus v2.4.2 |
| 图数据库 | Neo4j v5 |
| 鉴权 | JWT RS256（golang-jwt v5）+ GitHub OAuth |
| 可观测性 | Zap · OpenTelemetry v1.21 · Prometheus |
| LLM | 通义千问（Qwen）· 智谱 AI（Zhipu）· OpenAI 兼容 |
| 前端 | React 18 · Vite · Ant Design 5 · TanStack Query · Zustand |

## 快速开始

### 前置要求

- Go 1.25+
- Node.js 18+ / npm
- Docker + Docker Compose（本地基础设施）

### 启动

```bash
# 1. 克隆并装依赖
git clone https://github.com/byteBuilderX/stratum.git
cd stratum
cp .env.example .env          # 填入 GitHub OAuth、LLM API Key 等

# 2. 拉起本地基础设施（Postgres · Redis · NATS · Milvus · Neo4j）
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
├── internal/                 8 个 bounded context（DDD 分层）
│   ├── agent/                ReAct 编排 · A2A 协议 · ChatStore · ExecutionStore
│   ├── memory/               记忆持久化 · 向量检索 · JetStream Pipeline
│   ├── knowledge/            GraphRAG · 文档摄取 · Neo4j 实体图谱
│   ├── skill/                Skill CRUD · AtomicEngine · PipelineEngine · CircuitBreaker
│   ├── mcp/                  MCP 服务器管理 · SkillAdapter
│   ├── iam/                  Tenant · Admin · OAuth · JWT · Onboard
│   ├── llmgateway/           LLM 统一网关 · TenantGatewayCache · 嵌入服务
│   └── platform/             Viper 配置 · Harness 生命周期管理
├── pkg/                      无业务抽象（storage · messaging · observability · ...）
├── cmd/server/               服务入口（≤30 行）
├── web/                      React 前端（src/modules 按域组织）
├── docs/                     架构 · 部署 · 模块文档
├── helm/ k8s/ grafana/       部署与可观测性配置
└── openspec/                 OpenSpec 规范变更记录
```

详细分层职责见 [`CLAUDE.md`](CLAUDE.md) 与 [`docs/agent/project.md`](docs/agent/project.md)。

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
make fe-build                 # vite build
make fe-dev                   # vite dev server

# 基础设施
make dev-up / dev-down        # 启停 infra + observability
make obs-up / obs-down        # 仅启停 Prometheus / Grafana / Jaeger

# 部署
make k8s-deploy               # kubectl apply
make helm-install             # helm install
make ci-cd-full               # 全量 CI + 部署到 dev

make help                     # 完整命令清单
```

## 文档

### 架构与约定

- [`CLAUDE.md`](CLAUDE.md) — 项目铁律：DDD 分层、命名、日志、安全
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
| 前端架构 | [`web/ARCHITECTURE.md`](web/ARCHITECTURE.md) · [`web/README.md`](web/README.md) |

### 规范变更

[`openspec/`](openspec/) — 按 OpenSpec 流程管理的规范变更提案与归档。

## 路线图

- [ ] Agent 多步骤规划（Plan-and-Execute）
- [ ] Workflow 引擎（DAG + 长任务调度）
- [ ] 更多 LLM 提供商（Anthropic · Ollama · 本地推理）
- [ ] 端到端 OTEL Trace 串联（HTTP → Agent → Skill → LLM）
- [ ] WASM Sandbox 替代 goja（Skill 沙箱执行）

## 贡献

欢迎 Issue 与 PR。提交前请阅读 [`CONTRIBUTING.md`](CONTRIBUTING.md) 与 [`CLAUDE.md`](CLAUDE.md)，确保：

1. 通过 `make be-fmt be-lint be-test`
2. 前端改动通过 `make fe-lint fe-build`
3. PR 标题格式：`[type](scope): description`（type: feat/fix/refactor/perf/test/docs/chore/ci）
4. PR 描述包含 **What · Why · HowToTest** 三段

## 协议

[Apache License 2.0](LICENSE)

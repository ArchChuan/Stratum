# Stratum 文档导航

> 当前事实基线：`37d8f05`（2026-07-21）。代码、测试与部署清单优先于文档描述。

## Agent 上下文入口

- Codex 自动加载适用范围内的 `AGENTS.md`；根文件由 `docs/agent/instructions.md` 和对应模板生成。
- Claude Code 通过根或子目录 `CLAUDE.md` 的 `@...` 引用加载选定的 `docs/agent/*.md`。
- `docs/agent/` 是按任务引用的模块上下文，不因文件位于该目录就全部常驻。
- 其他 `docs/` Markdown 是人工导航或任务证据，只有被入口引用或为当前任务主动打开时才进入上下文。

## 新用户

- [项目概览](../README.md)：能力、架构、技术栈和快速开始。
- [本地开发](local-dev.md)：本地依赖、后端和前端启动。
- [启动指南](STARTUP_GUIDE.md)：完整启动顺序与故障定位。
- [LLM 快速开始](QUICKSTART_LLM.md)：配置并验证模型调用。
- [MCP 快速开始](mcp-quickstart.md)：配置并验证 MCP 服务。

## 开发与架构

- [当前规格](SPEC.md)：系统能力、上下文和主要契约。
- [工程标准](engineering-standards.md)：分层、错误、日志和安全约束。
- [项目事实](agent/project.md)：依赖版本与目录职责。
- [架构规则](agent/architecture.md)：DDD 边界和依赖方向。
- [包架构索引](go-package-architecture/README.md)：Go 包职责与依赖。
- [架构演化](architecture/EVOLUTION.md)：架构决策的演变过程。

## 部署与运维

- [部署指南](DEPLOYMENT_GUIDE.md)：部署方式和生产检查。
- [K3s Demo](deployment/k3s-demo.md)：当前公网 HTTP Demo 部署。
- [Helm 指南](deployment/HELM_GUIDE.md)：Chart 参数和发布操作。
- [CI/CD 指南](deployment/CI_CD_GUIDE.md)：流水线、环境和回滚。
- [部署架构](agent/deployment-architecture.md)：公网、Traefik 与集群端口关系。

当前 Demo 的公网入口是 <http://101.200.181.141:6879>，健康检查是
<http://101.200.181.141:6879/api/health>。该 profile 使用公网 IP 和明文 HTTP，
不要求域名或 HTTPS；公网 `6879` 由 Traefik `web2` 接收，再转发到集群内部 HTTP 服务。

## 文档职责

- `docs/agent/`：Agent 可按任务直接引用的当前约束和项目事实。
- `docs/go-package-architecture/`：生成的包级代码参考，不作为常驻指令。
- 顶层及 `deployment/`、`operations/`：面向开发者和运维人员的当前指南。
- `audits/`、`superpowers/specs/`、`superpowers/plans/`：带日期的历史证据，不作为当前运行手册。

## 历史记录

- `audits/` 保存特定日期的审计结果，不代表当前缺陷状态。
- `superpowers/specs/` 保存设计决策，不作为运行手册。
- `superpowers/plans/` 保存实施计划和完成记录，不作为当前命令参考。

历史文档应结合其日期和提交版本阅读；发现与当前代码冲突时，以当前实现和测试为准。

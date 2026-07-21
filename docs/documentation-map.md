# Stratum 文档导航

> 当前事实基线：`37d8f05`（2026-07-21）。代码、测试与部署清单优先于文档描述。

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

## 模块资料

`agent/` 下的 Agent、API、Memory、Knowledge、Milvus、NATS、Observability 等文档是当前开发约束。
LLM、MCP、持久化和本地 CI/CD 的专题指南位于 `docs/` 顶层。

## 历史记录

- `audits/` 保存特定日期的审计结果，不代表当前缺陷状态。
- `superpowers/specs/` 保存设计决策，不作为运行手册。
- `superpowers/plans/` 保存实施计划和完成记录，不作为当前命令参考。

历史文档应结合其日期和提交版本阅读；发现与当前代码冲突时，以当前实现和测试为准。

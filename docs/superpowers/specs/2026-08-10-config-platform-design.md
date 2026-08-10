# 配置管理系统设计（Nacos 平台化）

日期：2026-08-10
状态：设计评审中
范围：引入开源配置中心 Nacos，将系统配置平台化、产品化

## 1. 背景与动机

远端生产环境排查发现 `https://101.200.181.141:8443/memory` 记忆条数始终为空。根因：`MEMORY_PIPELINE_ENABLED` 在远端部署配置中从未设置，开关默认 false，记忆写入 pipeline（outbox → NATS → embed → enrich → memory_entries）从未装配，33 条记忆堆积在 `memory_outbox` 未落库。

进一步盘点：系统共有 39 个 `getEnv` 配置项散落在 `config/config.go`，另有 31 个行为常量散落 `pkg/constants/`。配置无平台、无审计、无热生效、无变更记录——`MEMORY_PIPELINE_ENABLED` 这类开关开没开、谁开的、什么时候开的，完全不可查。

目标：引入配置管理系统，把配置平台化、产品化，让业务/功能配置可管理、可审计、可热生效。

## 2. 约束

1. **不自研**：使用现成开源配置中心，禁止重复造轮子。
2. **只有业务和功能配置透出**：基础设施配置（连接串、密钥、部署 env）保持 configmap/secret 现状不动。
3. **只有不被评测/进化链路修改以提升 agent 表现效果的配置项才放入平台**：表现域参数（prompt、模型、温度、检索阈值等影响 agent 输出质量的）由进化链路（`internal/evaluation` 的 ResourceRevision + Experiment 状态机）管理，不进平台，防止双写竞争。

## 3. 选型结论：Nacos 2.x

判定逻辑（现成平台 + 官方 Go SDK + 热生效 + 合规 的交集）：

| 候选 | 现成平台 | Go SDK | 热生效 | License / 合规 | 结论 |
|---|---|---|---|---|---|
| **Nacos 2.x** | ✅ standalone 服务端 | ✅ nacos-sdk-go v2（gRPC 推送） | ✅ 秒级 | Apache-2.0，命名空间/审计/灰度 | **选用** |
| Apollo | ✅ | ⚠️ 社区 SDK，30s 轮询 | ⚠️ 慢 | Apache-2.0，但 4 个 Java 组件 + 强制 MySQL，重 | 排除 |
| Consul | ✅ | 官方 SDK | ✅ | BUSL 1.1（IBM licensor），无命名空间/灰度/审计 | 排除 |
| etcd | ⚠️ 仅存储无平台 | 官方 SDK | ✅ | Apache-2.0 | 排除 |

**成本**：Nacos 服务端是 JVM 进程 + 需要独立 MySQL。若 JVM 不可接受，备选路径是 etcd + 薄控制面（自研 UI/审计）或 Consul，但前者违反"不自研"约束，后者有许可风险。本设计接受 Nacos 的成本。

## 4. 架构

### 4.1 配置分层（三层）

```
┌───────────────────────────────────────────────┐
│ 层 1：Nacos 平台（档位 A 业务/功能配置）          │
│   人工管理、热生效、可审计、可回滚                  │
├───────────────────────────────────────────────┤
│ 层 2：stratum config 层（config/config.go）     │
│   Nacos 值优先覆盖 env，env 为兜底                │
├───────────────────────────────────────────────┤
│ 层 3：configmap/secret（档位 C 基础设施，不动）    │
│   连接串、密钥、部署参数                           │
└───────────────────────────────────────────────┘
```

### 4.2 配置项归属三档划分

| 档位 | 归属 | 判定 | 示例 |
|---|---|---|---|
| A | Nacos 平台 | 非基础设施 + 非表现域 | 功能开关、pipeline 工程参数、业务配置 |
| B | 进化链路域（DB ResourceRevision + objectstore） | 表现域：可被评测/进化调优以提升 agent 表现 | prompt 模板、模型选择、temperature、topK、表现阈值 |
| C | configmap/secret | 基础设施 | 连接串、密钥 |

**判定两问**（新配置项进 Nacos 前必答）：

1. 是基础设施吗？（连接串/密钥/部署 env）→ 档位 C，不进。
2. 影响 agent 表现效果吗？（prompt/模型/温度/表现阈值，可能被评测调优）→ 档位 B，不进。
3. 都否 → 档位 A，可进 Nacos。

**边界依据**：进化链路（`internal/evaluation`）通过 Experiment 状态机（pending → running → canary 5→20→50→100 → promote/rollback）对 ResourceRevision（draft → published，payload 存 objectstore）进行灰度发布。表现域参数若同时被 Nacos 推送和进化链路发布修改，两个系统互相覆盖、状态分裂。故档位 B 参数保持 env 现状，归属权划给进化链路域，由进化链路未来接管。

### 4.3 Nacos 数据模型

- 1 个 production namespace（如 `stratum-prod`）。
- dataId 按业务域拆分：`stratum/memory`、`stratum/auth`、`stratum/agent` 等，每域一个 dataId，变更粒度小、审计清晰。
- group 使用 `DEFAULT_GROUP`。
- 配置格式：每个 dataId 一个 JSON 文档，字段与 `config/config.go` 结构体对应（JSON 与结构体直接 Marshal/Unmarshal，零额外依赖）。

### 4.4 热生效机制

```
Nacos listener（gRPC 推送）
  → 解析新值
  → 校验合法（类型/范围）
  → RW 锁原子替换 Config 副本
  → 轮询型组件（OutboxPoller 等）每轮循环 re-read Config
    —— 无需复杂通知链，天然热生效
```

- **fail-closed 语义**：启动时 Nacos 不可达 → 明确 WARN 日志 + 用 env/默认值启动（不阻塞，不静默）；运行中断连 → SDK 自动重连，保留 last-known 值不重置回 env；非法值 → 回退旧值并告警。
- **密钥边界**：档位 C 密钥类永不经过 Nacos，仅走 secret。

## 5. 部署设计

```
远端 k3s（单节点）namespace: stratum
├── nacos ── standalone 模式（单实例，不集群）
│   └── 存储：MySQL 8 独立小实例，PVC 持久化
│       （不用现有 PostgreSQL：Nacos 官方 datasource 只支持
│         MySQL/Derby；Derby 官方明示仅开发用；PG 需第三方
│         plugin，维护风险。单实例 MySQL 不引入高可用。）
│   └── UI 仅内网可达（复用现有 ingress/NodePort 模式），
│       账号密码保护，不对外网
└── stratum-server（现有 deployment 不变）
    └── 新增 env：NACOS_URL、NACOS_NAMESPACE、NACOS_USER
        （NACOS_PASSWORD 走 secret）
```

## 6. 迁移路径（三步，每步可独立验收）

| 阶段 | 内容 | 验收 |
|---|---|---|
| 1 | 部署 Nacos（MySQL + PVC + 账号）；config 包接入 Nacos（Nacos 优先、env 兜底）；迁移档位 A 首批（auth 开关、pipeline 工程参数） | 改 Nacos 值 → 远端日志确认热生效；Nacos 断掉 → 服务照常启动（WARN 明确） |
| 2 | `MEMORY_PIPELINE_ENABLED=true` 放入 Nacos → pipeline 装配 → OutboxPoller at-least-once 消化 33 条堆积 → 落库 `memory_entries` | `/memory` 页面条数 > 0；`memory.embed.success`/enrich 日志出现；outbox 归零 |
| 3 | 治理落地：配置清单文档（负责人 + 判定两问）、变更流程（审批 + UI 截图留存） | 盘点文档入库 `docs/` |

## 7. 配置项迁移清单（三档）

### 档位 A：迁移 Nacos（首批）

| 配置项 | 说明 |
|---|---|
| MEMORY_PIPELINE_ENABLED | 记忆 pipeline 开关（本次事故根因） |
| MEMORY_PIPELINE_POLL_INTERVAL / BATCH_SIZE / EMBED_WORKERS / ENRICH_WORKERS / EMBED_ACK_WAIT / ENRICH_ACK_WAIT / MAX_DELIVER | pipeline 工程参数（调度节奏，不影响记忆质量） |
| PASSWORD_AUTH_ENABLED / GUEST_AUTH_ENABLED | 认证功能开关 |

### 档位 B：表现域，保持 env（不进 Nacos，未来进化链路接管）

| 配置项 | 说明 |
|---|---|
| GLOBAL_AGENT_SYSTEM_PROMPT | agent 系统 prompt |
| MEMORY_ENRICH_MODEL / SUMMARY_MODEL | 记忆 enrich/summary 模型选择 |
| MEMORY_ENRICHMENT_PROMPT / SUMMARY_PROMPT | 记忆 enrich/summary prompt |
| MEMORY_SUMMARY_TOKEN_THRESHOLD | 摘要触发阈值 |
| RERANK_*（模型、阈值、topK） | 检索重排表现参数 |
| temperature、topK 等表现类参数 | 后续新增同类一律归 B |

### 档位 C：保持 configmap/secret 不动

所有连接串（DATABASE / NATS / REDIS / MILVUS / OPIK / S3 URL）与全部密钥类（JWT_PRIVATE_KEY_PEM、DATA_ENCRYPTION_KEY、GITHUB_CLIENT_SECRET、OPIK_API_KEY、STRATUM_ADMIN_PASSWORD、RERANK_API_KEY、TRACE_PAYLOAD_ACCESS_KEY/SECRET_KEY）。

## 8. 测试

- 单元：加载优先级（Nacos > env > default）、listener 原子替换、非法值回退旧值。
- 集成：Nacos 不可达 fail-closed、断连重连保留 last-known、热更新对轮询组件生效。
- E2E：阶段 2 验收即系统级验证（堆积记忆消化、页面条数 > 0）。

## 9. 治理机制（防无序膨胀）

每个进 Nacos 的配置项必须有：

- **负责人**：谁改、改什么、为什么可查。
- **变更审批**：变更走审批流程，UI 截图留存审计。
- **过期清理**：废弃配置定期清理，平台支持多版本回滚。
- **新增审批**：新配置项进 Nacos 前必须通过判定两问。

## 10. 明确不做（YAGNI）

- Nacos 集群/高可用（单节点 k3s，standalone 足够）。
- 配置灰度发布（Nacos 本身有发布能力，但当前规模不需要区分环境）。
- 权限细粒度 RBAC（账号密码 + namespace 隔离足够，需要时 Nacos 原生支持再开）。
- 基础设施配置迁入平台（约束 2 明确排除）。

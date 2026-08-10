# 配置管理

## 分层

- 档位 A：Nacos（业务/功能配置，人工管理，热生效，可审计）——namespace `stratum-prod`，group `DEFAULT_GROUP`
- 档位 B：进化链路域（DB ResourceRevision + objectstore）——表现域参数保持 env 现状，未来由进化链路接管
- 档位 C：configmap/secret（基础设施/密钥，永不进 Nacos）

## 判定两问（新配置项归档前必答）

1. 是基础设施吗？（连接串/密钥/部署 env）→ 档位 C，不进。
2. 影响 agent 表现效果吗？（prompt/模型/温度/表现阈值，可能被评测调优）→ 档位 B，不进。
3. 都否 → 档位 A，可进 Nacos。

## 配置清单

### 档位 A（Nacos dataId → 字段）

| dataId | 字段 | 热生效 | 负责人 |
|---|---|---|---|
| stratum/auth | password_auth_enabled, guest_auth_enabled | 冷（重启） | 平台 |
| stratum/memory | enabled（冷）；poll_interval, batch_size（热） | 混合 | 平台 |

### 档位 B（保持 env，未来进化链路接管）

GLOBAL_AGENT_SYSTEM_PROMPT、MEMORY_ENRICH_MODEL/SUMMARY_MODEL、
MEMORY_ENRICHMENT_PROMPT/SUMMARY_PROMPT、MEMORY_SUMMARY_TOKEN_THRESHOLD、
RERANK_*、temperature/topK 等表现类参数。

### 档位 C（configmap/secret）

全部连接串（DATABASE/NATS/REDIS/MILVUS/OPIK/S3 URL）与密钥类
（JWT_PRIVATE_KEY_PEM、DATA_ENCRYPTION_KEY、GITHUB_CLIENT_SECRET、
OPIK_API_KEY、STRATUM_ADMIN_PASSWORD、RERANK_API_KEY、
TRACE_PAYLOAD_ACCESS_KEY/SECRET_KEY）。

## 变更流程

1. 新配置项：先答判定两问 → 档位 A 才允许建 dataId。
2. 改配置：Nacos UI 操作 → 变更前截图留存 → 观察热生效日志（config: nacos push applied）。
3. 过期清理：废弃 dataId 定期盘点移除；Nacos 保留多版本回滚。
4. 责任人：每个 dataId 有负责人（上表）。

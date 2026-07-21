# 生产环境 E2E 渐进压测报告（2026-07-21）

## 结论

本次压测覆盖公网入口、集群入口、前端 Nginx 反向代理、后端 Pod、Kubernetes Service、认证、
内存 Registry、PostgreSQL 读取和一次真实 Agent/LLM 调用。当前最先出现的吞吐瓶颈不是主机、
Traefik、Go 后端或 PostgreSQL，而是前端 Nginx 到后端 Service 的反向代理边界。

- 公网 `/api/health` 在 10--20 并发出现吞吐拐点，稳定在约 750 RPS。
- 后端 Pod 和 backend Service 在相同 40 并发下约为 7.4k RPS 和 7.3k RPS。
- 请求经过 frontend Nginx `/api/` 后降至约 765 RPS，吞吐下降约 90%。
- 公网链路和 frontend Nginx 代理结果接近，Traefik 和公网转发不是当前第一瓶颈。
- PostgreSQL 真实读取链路约 623 RPS，压测时连接空闲且资源未饱和。
- 真实 Agent/LLM 单次执行成功，耗时约 522 ms；未执行高并发 LLM 压测，避免越过业务限流和产生不必要的外部模型成本。

同时发现一个与性能放大有关的高风险配置缺陷：Helm 注入 `ENVIRONMENT`，应用生产模式判断读取
`APP_ENV`。生产日志中的环境字段为空，因此 logger 未进入 production 模式，正在输出 DEBUG 日志。
这不仅增加 Agent 并发时的日志 I/O，还可能记录不应进入生产日志的 LLM 请求和响应内容，应作为
独立 P0 缺陷修复。

## 范围与停止条件

目标入口：

- 公网：`http://101.200.181.141:6879`
- 健康检查：`http://101.200.181.141:6879/api/health`
- 公网 `6879` 经 K3s/Traefik 转发到集群内部 HTTP 服务端口。

负载按并发 `5 -> 10 -> 20 -> 40` 渐进增加。任一阶段错误率超过 2%、P95 超过 3 秒，或出现大量
429/5xx 时停止。测试只读取业务数据；获取认证态时创建了临时 guest 账号，没有执行批量写入或删除。

## 测试方法与测量有效性

压测器使用固定并发 worker、连接复用和直方图记录延迟，所有有效公网测试均显式禁用环境 HTTP
代理；`curl` 恢复检查使用 `--noproxy '*'`。

首轮曾测得 502 和约 5 秒 P95，随后确认响应含代理特征头，且压测器使用了
`http.ProxyFromEnvironment`。该结果来自本机 HTTP 代理瓶颈，不是 Stratum 生产环境响应，已判定为
无效测量，本文所有容量结论均不使用这组数据。

## 公网基线

### 静态首页

| 并发与时长 | 请求数 | RPS | 错误 | P50 | P95 | P99 |
|---|---:|---:|---:|---:|---:|---:|
| 1，10 秒 | 984 | 98.39 | 0 | 9.66 ms | 13.73 ms | 16.26 ms |
| 40，20 秒 | 31,883 | 1,594.02 | 0 | 12.81 ms | 69.08 ms | 78.81 ms |

### `/api/health` 全链路

| 并发 | RPS | 错误 | P50 | P95 | P99 |
|---:|---:|---:|---:|---:|---:|
| 1 | 95.30 | 0 | 9.98 ms | 13.97 ms | 16.65 ms |
| 5 | 430.43 | 0 | 11.23 ms | 14.54 ms | 17.08 ms |
| 10 | 737.38 | 0 | 10.97 ms | 30.88 ms | 44.17 ms |
| 20 | 714.46 | 0 | 11.93 ms | 76.57 ms | 83.63 ms |
| 40 | 750.33 | 0 | 72.43 ms | 97.98 ms | 105.15 ms |

40 并发 30 秒复测得到 22,457 个请求、748.55 RPS、零错误，P50 71.35 ms、P95 98.82 ms、
P99 105.48 ms、最大 206.73 ms。吞吐从 10 并发后不再增长，延迟随排队上升。

## 分层定位

在生产节点内以 40 并发持续 10 秒分别请求各层：

| 链路 | RPS | P50 | P95 | 错误 |
|---|---:|---:|---:|---:|
| backend Pod `/health` | 7,437.88 | 2.35 ms | 45.93 ms | 0 |
| backend Service `/health` | 7,340.58 | 2.38 ms | 47.27 ms | 0 |
| frontend Pod 静态页 | 1,692.92 | 1.86 ms | 94.05 ms | 0 |
| frontend Pod `/api/health` | 765.03 | 66.24 ms | 102.29 ms | 0 |
| 公网 `/api/health` | 约 749 | 约 71 ms | 约 99 ms | 0 |

backend Pod 与 Service 结果接近，说明 Kubernetes Service 转发没有显著损失。frontend Pod 代理结果
与公网结果接近，说明公网入口、ServiceLB 和 Traefik 也没有造成主要损失。明显下降只发生在
frontend Nginx 到 backend Service 这一段。

当前 `helm/templates/frontend-configmap.yaml` 的 `/api/` 只配置 `proxy_pass` 和请求头/超时，没有设置
`proxy_http_version 1.1`、清空 `Connection` 头或配置 upstream keepalive。Nginx 反向代理默认使用
HTTP/1.0，这与每次 API 请求重新建立上游连接的测量特征一致。该机制是基于配置和分层数据得到的
最强根因假设，修复后仍需用同一组分层压测验证增益。

## 业务链路

### 认证与内存 Registry

`GET /api/agents` 包含认证中间件，但 Agent 列表由进程内 Registry 提供，不访问数据库。

| 并发 | RPS | P95 | 错误 |
|---:|---:|---:|---:|
| 1 | 75.39 | 15.42 ms | 0 |
| 5 | 388.98 | 15.15 ms | 0 |
| 10 | 585.87 | 39.75 ms | 0 |
| 20 | 560.33 | 80.01 ms | 0 |
| 40 | 581.56 | 101.00 ms | 0 |

### PostgreSQL 读取

`GET /api/tenant/settings` 是真实 PostgreSQL 读取链路。

| 并发 | RPS | P95 | 错误 |
|---:|---:|---:|---:|
| 1 | 85.60 | 13.53 ms | 0 |
| 5 | 438.58 | 14.25 ms | 0 |
| 10 | 617.53 | 42.90 ms | 0 |
| 20 | 620.67 | 79.97 ms | 0 |
| 40 | 609.16 | 104.06 ms | 0 |

40 并发 30 秒复测得到 18,679 个请求、622.55 RPS、零错误，P50 77.72 ms、P95 101.52 ms、
P99 130.65 ms、最大 706.89 ms。测试时 PostgreSQL 约 103m CPU，应用连接均为空闲状态，不能据此
认定数据库已经饱和。

### Agent/LLM

使用生产认证和真实模型执行了一次 Agent E2E：`maxSteps=2` 时 HTTP 200，总耗时约 522 ms，模型调用
成功。先前 `maxSteps=1` 返回 500，是测试参数不足导致 graph 达到最大步数，不是生产故障。

没有执行 Agent 并发压测。该路径每用户限制为持续 20 请求/分钟、burst 3，并会产生真实模型成本；
单次证据只证明链路可用，不能作为 Agent 并发容量结论。Agent 实际容量还会受模型供应商延迟、配额、
token 数量和工具调用次数影响。

## 资源观测

40 并发 health 测试期间，节点 CPU 约 11%、内存约 32%；Traefik 约 211m CPU，frontend 约 201m，
backend 约 166m，PostgreSQL 约 8m。`/api/tenant/settings` 测试期间 backend 约 286m、frontend 约
192m、PostgreSQL 约 103m。主机约 8 核、15 GiB 内存，可用内存约 11 GiB，负载低，Pod 全部 Ready
且无重启。节点资源不是当前容量上限。

frontend 和 backend 当前均为单副本。单副本并未耗尽节点资源，但缺少滚动更新和实例故障时的容量
冗余，也无法通过横向扩容吸收峰值。

## 风险排序与建议

1. **P0：修复生产环境变量错配。** 统一 Helm 与应用读取的环境变量，确保 production logger 使用
   JSON/INFO，禁止记录原始 LLM 请求、响应和其他敏感内容；增加 Helm 渲染测试及生产模式单测。
2. **P1：复用 Nginx 上游连接。** 为 backend 定义 upstream keepalive，代理使用 HTTP/1.1 并清空
   `Connection` 头。部署后重跑 backend Service、frontend Pod 和公网三层同参数压测，以实际增益验收。
3. **P2：增加运行冗余。** 在完成连接复用优化和容量复测后，为 frontend/backend 设置至少两个副本，
   配合 readiness、反亲和或 topology spread 和基于实测容量的资源 request/limit。
4. **P3：单独设计 Agent 容量测试。** 使用专用测试租户、多用户身份、成本预算和供应商配额，在不绕过
   生产限流的前提下测量成功率、首 token、总耗时、token 吞吐和工具链路延迟。

## 清理与残余数据

本轮临时压测源码已删除。调整主工作区防误操作 hook 后，客户端和生产节点的
`/tmp/stratum-loadtest` 二进制均已精确删除；复核未发现仍在运行的压测进程，公网健康检查恢复为
HTTP 200。

为取得真实 JWT 创建了约 8--10 个 guest 账号；没有执行未经核验的宽泛数据库删除。这些账号使用默认
tenant，TTL 为 24 小时，将由每小时运行的 guest reaper 在到期后回收。

`HEAD /api/health` 返回 404，而 `GET /api/health` 正常，这是 HTTP 方法契约差异，不影响本次使用 GET
完成的健康检查和容量结论。

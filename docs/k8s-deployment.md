# K8s 自动化部署配置指南

## 前置要求

### 1. 创建 GitHub 环境（Environments）

在 GitHub 仓库 Settings → Environments 中创建三个环境：

- **development**: 用于开发环境
- **staging**: 用于测试/预发布环境  
- **production**: 用于生产环境

### 2. 配置 GitHub Secrets

为每个环境添加以下 Secrets：

#### 所有环境共需

```bash
SLACK_WEBHOOK_URL: https://hooks.slack.com/services/YOUR/WEBHOOK/URL
```

#### 各环境分别需要

**Development (develop 分支)**

```bash
KUBECONFIG_DEV: <base64 encoded kubeconfig>
```

**Staging (main 分支)**

```bash
KUBECONFIG_STAGING: <base64 encoded kubeconfig>
```

**Production (main 分支，需要手动审批)**

```bash
KUBECONFIG_PROD: <base64 encoded kubeconfig>
```

### 3. 生成 Kubeconfig 的 Base64 编码

```bash
# 从现有 kubeconfig 获取
cat ~/.kube/config | base64 -w 0 | pbcopy

# 或者从集群导出
kubectl config view --raw --flatten | base64 -w 0 | pbcopy
```

### 4. k8s 集群准备

#### 创建 namespace

```bash
kubectl create namespace clawhermes
```

#### 创建 ConfigMap

```bash
kubectl create configmap clawhermes-config \
  --from-literal=PORT=8080 \
  --from-literal=LOG_LEVEL=info \
  --from-literal=ENVIRONMENT=dev \
  --from-literal=NATS_URL=nats://nats:4222 \
  --from-literal=MILVUS_HOST=milvus \
  --from-literal=MILVUS_PORT=19530 \
  --from-literal=OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317 \
  --from-literal=OTEL_SERVICE_NAME=clawhermes-ai \
  --from-literal=OTEL_EXPORTER_TYPE=otlp \
  --from-literal=OTEL_SAMPLING_RATIO=0.1 \
  -n clawhermes
```

#### 创建 Secret

```bash
kubectl create secret generic clawhermes-secrets \
  --from-literal=OPENAI_API_KEY=<api-key> \
  --from-literal=JWT_SECRET=<jwt-secret> \
  -n clawhermes
```

#### 应用 k8s manifests

```bash
# 应用所有基础配置
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/secret.yaml
kubectl apply -f k8s/security.yaml
kubectl apply -f k8s/network-policy.yaml

# 应用依赖服务
kubectl apply -f k8s/dependencies.yaml

# 应用应用部署
kubectl apply -f k8s/deployment.yaml

# 应用监控
kubectl apply -f k8s/monitoring.yaml
```

或使用 Helm：

```bash
helm install clawhermes ./charts/clawhermes-ai \
  -n clawhermes \
  -f values-dev.yaml
```

## 部署流程

### 自动部署流程

1. **开发分支 (develop)**
   - 推送代码到 `develop` 分支
   - CI/CD 自动运行测试、构建镜像
   - 自动部署到 Dev 环境
   - 自动验证部署

2. **主分支 (main) - Staging**
   - 推送代码到 `main` 分支（通常通过 PR 合并）
   - CI/CD 自动运行完整测试套件
   - 自动构建并推送镜像
   - 自动部署到 Staging 环境
   - 自动验证部署

3. **主分支 (main) - Production**
   - 在 Staging 部署成功后触发 Production 部署
   - **需要手动审批**（GitHub Environment Protection Rules）
   - 运行部署前检查
   - 备份当前部署
   - 更新镜像并进行滚动更新
   - 验证新部署
   - 运行部署后检查

### 手动部署

#### 部署到特定环境

```bash
# Dev 环境
kubectl set image deployment/clawhermes-ai \
  clawhermes-ai=ghcr.io/YOUR_ORG/clawhermes-ai:latest \
  -n clawhermes

# 等待部署完成
kubectl rollout status deployment/clawhermes-ai -n clawhermes
```

#### 查看部署状态

```bash
# 查看 Deployment
kubectl get deployment clawhermes-ai -n clawhermes

# 查看 Pods
kubectl get pods -n clawhermes -l app=clawhermes-ai

# 查看详细信息
kubectl describe deployment clawhermes-ai -n clawhermes

# 查看滚动更新历史
kubectl rollout history deployment/clawhermes-ai -n clawhermes
```

#### 回滚部署

```bash
# 回滚到上一个版本
kubectl rollout undo deployment/clawhermes-ai -n clawhermes

# 回滚到特定版本
kubectl rollout undo deployment/clawhermes-ai -n clawhermes --to-revision=3

# 使用脚本回滚
bash scripts/deploy/rollback.sh clawhermes 3
```

#### 查看日志

```bash
# 查看最新 Pod 的日志
kubectl logs -n clawhermes -l app=clawhermes-ai --tail=100 -f

# 查看特定 Pod 的日志
kubectl logs -n clawhermes POD_NAME --tail=100 -f

# 查看之前的容器日志（崩溃情况）
kubectl logs -n clawhermes POD_NAME --previous
```

## 监控和告警

### Prometheus 指标

访问 Prometheus：

```bash
kubectl port-forward -n clawhermes svc/prometheus 9090:9090
# 访问 http://localhost:9090
```

关键指标：

- `http_requests_total`: HTTP 请求总数
- `http_request_duration_seconds`: 请求延迟
- `deployment_replicas_available`: 可用副本数
- `container_memory_usage_bytes`: 内存使用量

### Grafana 仪表板

访问 Grafana：

```bash
kubectl port-forward -n clawhermes svc/grafana 3000:3000
# 访问 http://localhost:3000 (admin/admin)
```

### Jaeger 分布式追踪

访问 Jaeger UI：

```bash
kubectl port-forward -n clawhermes svc/jaeger 16686:16686
# 访问 http://localhost:16686
```

## 故障排查

### 部署卡住不前进

```bash
# 检查事件
kubectl describe deployment clawhermes-ai -n clawhermes

# 查看 Pod 状态
kubectl get pods -n clawhermes -o wide

# 查看 Pod 详情
kubectl describe pod POD_NAME -n clawhermes

# 查看日志
kubectl logs POD_NAME -n clawhermes
```

### Pod 无法启动

```bash
# 检查镜像是否存在
kubectl describe pod POD_NAME -n clawhermes | grep -i image

# 检查资源限制
kubectl describe node NODE_NAME

# 查看之前的日志
kubectl logs POD_NAME -n clawhermes --previous
```

### 高 CPU/内存使用

```bash
# 查看资源使用
kubectl top pods -n clawhermes

# 检查 HPA 状态
kubectl get hpa clawhermes-ai-hpa -n clawhermes

# 增加资源限制（编辑 deployment）
kubectl edit deployment clawhermes-ai -n clawhermes
```

## 最佳实践

1. **总是在 Staging 测试**
   - 在生产部署前先在 Staging 环境验证

2. **使用版本标签**
   - 不要使用 `latest` 标签，使用具体版本号
   - 便于快速回滚

3. **监控部署指标**
   - 部署前后查看 Prometheus 指标
   - 设置告警规则

4. **灰度部署**
   - 使用 Canary 或 Blue-Green 部署策略
   - 逐步推出新版本

5. **自动回滚**
   - 配置失败自动回滚
   - 设置合理的健康检查参数

6. **备份恢复**
   - CI/CD 自动备份部署配置
   - 保留回滚历史

## 常见问题

**Q: 如何跳过 Production 审批？**
A: 在 GitHub Environment Settings 中关闭 "Required reviewers"（不推荐）

**Q: 如何加速部署？**
A: 使用 Docker 层缓存，在 CI 中缓存依赖

**Q: 如何监控部署时间？**
A: 查看 CI/CD 日志中的时间戳，或在 Prometheus 中添加部署持续时间指标

**Q: 生产环境出现问题如何快速回滚？**
A: 执行 `kubectl rollout undo deployment/clawhermes-ai -n clawhermes`

## 相关文档

- [k8s Deployment 文档](k8s/deployment.yaml)
- [Helm Chart](charts/clawhermes-ai/)
- [CI/CD 工作流](.github/workflows/ci-cd.yml)
- [部署脚本](scripts/deploy/)

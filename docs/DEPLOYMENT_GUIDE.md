# 部署指南

本文档详细介绍了stratum项目的各种部署方式，包括本地开发环境、Kubernetes云原生环境和WSL环境。

## 部署架构

stratum项目采用云原生架构，底层依赖服务包括：

- **NATS** - 事件驱动消息队列系统
- **Milvus** - 向量数据库，用于向量存储与检索
- **ETCD** - 用于Milvus的分布式协调服务
- **MinIO** - 对象存储服务，供Milvus使用
- **OpenTelemetry Collector** - 用于收集日志、指标和追踪数据

## 前置要求

无论选择哪种部署方式，都需要满足以下基本要求：

- Go 1.25.0+
- Docker
- Make
- Git

根据部署方式的不同，还需要额外的工具：

- Kubernetes (kubectl) - 用于云原生部署
- Helm - 用于包管理
- (可选) WSL 2 - 用于 Windows 环境

## 部署方式

### 1. 本地开发部署

适用于开发和调试阶段。

#### 步骤

1. 克隆项目：

   ```bash
   git clone https://github.com/stratum/stratum.git
   cd stratum
   ```

2. 配置环境变量：

   ```bash
   cp .env.example .env
   # 根据实际环境编辑 .env 文件
   ```

3. 启动应用：

   ```bash
   ./start.sh
   ```

4. 验证服务：

   ```bash
   curl http://localhost:8080/health
   # 响应: {"status":"ok"}
   ```

5. 停止服务：

   ```bash
   ./stop.sh
   ```

#### 说明

本地开发部署模式只运行主应用，不包括任何底层依赖服务。在生产环境中使用时，需要单独部署所有依赖服务。

### 2. Kubernetes 部署

适用于生产环境的云原生部署。

#### 步骤

1. 构建 Docker 镜像：

   ```bash
   make docker-build
   ```

2. 部署依赖服务：

   ```bash
   kubectl apply -f k8s/dependencies.yaml
   ```

3. 等待依赖服务就绪：

   ```bash
   kubectl wait --for=condition=ready pod -l app=nats --timeout=120s
   kubectl wait --for=condition=ready pod -l app=milvus --timeout=120s
   ```

4. 部署主应用：

   ```bash
   kubectl apply -f k8s/deployment.yaml
   ```

5. 验证部署：

   ```bash
   kubectl get pods
   kubectl get services
   ```

6. 访问服务：

   ```bash
   # 端口转发到本地
   kubectl port-forward svc/stratum-service 8080:80
   ```

7. 停止部署：

   ```bash
   make k8s-delete
   ```

#### 验证服务

部署完成后，可以通过以下命令验证所有服务是否正常运行：

```bash
# 检查Pod状态
kubectl get pods

# 检查服务状态
kubectl get services

# 查看应用日志
kubectl logs -f deployment/stratum

# 查看依赖服务日志
kubectl logs -f deployment/nats
kubectl logs -f deployment/milvus
```

### 3. Helm 部署

Helm 是 Kubernetes 的包管理工具，提供更便捷的部署方式。

#### 步骤

1. 构建 Docker 镜像：

   ```bash
   make docker-build
   ```

2. 安装 Helm Chart：

   ```bash
   make helm-install
   ```

3. 验证部署：

   ```bash
   helm status stratum-release
   kubectl get pods
   ```

4. 卸载 Helm Release：

   ```bash
   make helm-uninstall
   ```

#### 自定义配置

可以通过编辑 [helm/values.yaml](file:///home/yang/go-projects/stratum/helm/values.yaml) 文件来自定义部署配置：

- 修改副本数量
- 调整资源限制
- 配置环境变量
- 设置持久卷参数

### 4. WSL 2 部署

适用于在 Windows 环境中开发和部署。

#### 前置条件

- WSL 2 已安装并配置
- Kubernetes 已在 WSL 或 Docker Desktop 中启用
- 已安装 kubectl 和 Helm

#### 步骤

1. 确保 WSL 环境已准备好：

   ```bash
   kubectl cluster-info
   helm version
   ```

2. 运行 WSL 部署脚本：

   ```bash
   ./wsl-start.sh
   ```

3. 验证部署：

   ```bash
   kubectl get pods
   kubectl get services
   ```

4. 停止部署：

   ```bash
   ./wsl-stop.sh
   ```

## 环境配置

### 配置文件

项目使用 [.env](file:///home/yang/go-projects/stratum/.env.example) 文件进行环境配置，主要配置项包括：

```env
# 服务配置
PORT=8080

# NATS 配置
NATS_URL=nats://nats:4222

# Milvus 配置
MILVUS_HOST=milvus
MILVUS_PORT=19530

NEO4J_PASSWORD=password

# OpenTelemetry 配置
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317

# LLM 配置
OPENAI_API_KEY=sk-your-openai-key
ANTHROPIC_API_KEY=sk-ant-your-anthropic-key
OLLAMA_ENDPOINT=http://localhost:11434
DEFAULT_LLM_PROVIDER=openai
```

### 服务发现

在 Kubernetes 环境中，服务通过内部 DNS 名称进行发现：

- NATS: `nats:4222`
- Milvus: `milvus:19530`
- OpenTelemetry Collector: `otel-collector:4317`

## 故障排除

### 通用问题

1. **端口冲突**
   - 检查 `.env` 文件中的端口配置
   - 确保没有其他服务占用相同端口

2. **依赖服务未就绪**
   - 使用 `kubectl get pods` 检查依赖服务状态
   - 使用 `kubectl logs <pod-name>` 查看详细日志

3. **Docker 镜像构建失败**
   - 确认 Docker 服务正在运行
   - 检查 Go 依赖是否正确

### Kubernetes 特有问题

1. **Pod 无法启动**

   ```bash
   kubectl describe pod <pod-name>
   kubectl logs <pod-name>
   ```

2. **服务不可达**
   - 检查 Service 配置
   - 验证网络策略是否允许流量

3. **持久卷问题**
   - 检查 PVC 状态
   - 确认存储类配置正确

### WSL 特有问题

1. **Kubernetes 集群不可用**
   - 检查 Docker Desktop Kubernetes 是否启用
   - 确认 kubectl 配置正确

2. **镜像无法加载**
   - 对于 Kind 集群：使用 `kind load docker-image stratum:latest`
   - 对于 Minikube：使用 `eval $(minikube docker-env)` 然后重新构建镜像

## 监控和可观测性

### 日志

应用使用 Uber Zap 记录结构化日志，可以通过以下方式查看：

```bash
# Kubernetes 环境
kubectl logs -f deployment/stratum

# 本地环境
./start.sh
```

### 指标

通过 OpenTelemetry 收集指标数据，包括：

- 请求延迟
- 错误率
- 吞吐量
- 技能执行统计

### 健康检查

应用提供健康检查端点：

```bash
curl http://localhost:8080/health
```

## 升级和维护

### 应用升级

对于 Kubernetes 部署，只需更新镜像标签并重新部署：

```bash
make docker-build
kubectl set image deployment/stratum stratum=stratum:new-version
```

### 依赖服务升级

依赖服务独立于主应用升级，可以通过更新 Kubernetes 部署配置来升级：

```bash
kubectl set image deployment/nats nats=nats:latest
```

### 回滚

如果升级出现问题，可以回滚到之前的版本：

```bash
kubectl rollout undo deployment/stratum
```

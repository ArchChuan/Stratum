# CI/CD 自动部署指南

## 🎯 架构总览

```
开发者推送代码 → CI/CD Pipeline → 镜像仓库 → Kubernetes 集群
```

## 📦 已配置文件

| 文件 | 用途 |
|------|------|
| `.github/workflows/deploy.yml` | GitHub Actions 工作流 |
| `.gitlab-ci.yml` | GitLab CI 工作流（可选） |
| `helm/values-prod.yaml` | 生产环境配置 |
| `helm/values-dev.yaml` | 开发环境配置 |

---

## 🔧 GitHub Actions 配置步骤

### Step 1: 配置镜像仓库

**阿里云容器镜像服务（推荐）**：

1. 登录阿里云控制台 → 容器镜像服务
2. 创建命名空间：`your-org`
3. 创建镜像仓库：`stratum-backend`、`stratum-frontend`
4. 获取访问凭证：
   - 仓库地址：`registry.cn-hangzhou.aliyuncs.com`
   - 用户名：阿里云账号
   - 密码：独立密码（需设置）

### Step 2: 获取 Kubernetes 凭证

**阿里云 ACK**：

```bash
# 1. 下载 kubeconfig
# 登录 ACK 控制台 → 集群列表 → 连接信息 → 复制 KubeConfig

# 2. 保存到本地
cat > ~/.kube/config-ack << 'EOF'
# 粘贴复制的内容
EOF

# 3. Base64 编码
cat ~/.kube/config-ack | base64 | tr -d '\n'
# 复制输出结果
```

### Step 3: 配置 GitHub Secrets

进入仓库页面：**Settings → Secrets and variables → Actions → New repository secret**

| Secret 名称 | 获取方式 | 示例值 |
|------------|---------|--------|
| `DOCKER_REGISTRY_URL` | 镜像仓库地址 | `registry.cn-hangzhou.aliyuncs.com` |
| `DOCKER_USERNAME` | 阿里云账号 | `your-aliyun-account` |
| `DOCKER_PASSWORD` | 镜像仓库独立密码 | `your-password` |
| `KUBE_CONFIG` | 上一步 base64 输出 | `YXBpVmVyc2lvbjogdjEKY2x1c3Rlcn...` |
| `POSTGRES_PASSWORD` | RDS 数据库密码 | `your-db-password` |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth 应用密钥 | `ghp_xxxxxxxxxxxxx` |
| `JWT_PRIVATE_KEY` | JWT 私钥 base64 | `LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBL...` |

**JWT 私钥编码**：

```bash
# 如果还没有私钥，先生成
openssl genrsa -out jwt_private.pem 2048

# Base64 编码
cat jwt_private.pem | base64 | tr -d '\n'
```

### Step 4: 修改配置文件

**更新 `helm/values-prod.yaml`**：

```yaml
# 修改镜像仓库地址
app:
  image:
    repository: registry.cn-hangzhou.aliyuncs.com/your-org/stratum-backend

frontend:
  image:
    repository: registry.cn-hangzhou.aliyuncs.com/your-org/stratum-frontend

# 修改数据库连接（使用实际 RDS 地址）
database:
  external: true
  host: "rm-xxxxx.postgres.rds.aliyuncs.com"  # 替换为实际地址
  port: 5432

# 修改 Redis 连接（使用实际 Redis 地址）
redis:
  external: true
  host: "r-xxxxx.redis.rds.aliyuncs.com"  # 替换为实际地址

# 修改域名
ingress:
  hosts:
    - host: api.yourdomain.com  # 替换为实际域名
  tls:
    - secretName: stratum-tls
      hosts:
        - api.yourdomain.com
```

### Step 5: 首次部署

```bash
# 1. 提交配置修改
git add helm/values-prod.yaml helm/values-dev.yaml .github/workflows/deploy.yml
git commit -m "feat(ci): add GitHub Actions deployment workflow"

# 2. 推送到 develop 分支（测试）
git checkout -b develop
git push origin develop

# 3. 观察 Actions 执行
# GitHub 页面 → Actions 标签 → 查看运行日志

# 4. 测试成功后推送到 main（生产）
git checkout main
git merge develop
git push origin main
```

---

## 🚀 GitLab CI 配置步骤

### Step 1: 配置 GitLab CI/CD Variables

进入项目页面：**Settings → CI/CD → Variables → Add variable**

| 变量名 | 值 | Protected | Masked |
|--------|-----|-----------|--------|
| `DOCKER_USERNAME` | 阿里云账号 | ✓ | ✓ |
| `DOCKER_PASSWORD` | 镜像仓库密码 | ✓ | ✓ |
| `KUBE_CONFIG` | kubeconfig base64 | ✓ | ✗ |
| `POSTGRES_PASSWORD` | 数据库密码 | ✓ | ✓ |
| `GITHUB_CLIENT_SECRET` | OAuth 密钥 | ✓ | ✓ |
| `JWT_PRIVATE_KEY` | JWT 私钥 base64 | ✓ | ✗ |

### Step 2: 启用 GitLab Runner

**使用 Shared Runners**：
- Settings → CI/CD → Runners → Enable shared runners

**或配置专用 Runner**：
```bash
# 1. 安装 gitlab-runner
curl -L https://packages.gitlab.com/install/repositories/runner/gitlab-runner/script.rpm.sh | sudo bash
sudo yum install gitlab-runner

# 2. 注册 Runner
sudo gitlab-runner register
# URL: https://gitlab.com
# Token: 从项目设置获取
# Executor: docker
# Default image: alpine:latest
```

### Step 3: 提交并触发

```bash
git add .gitlab-ci.yml helm/
git commit -m "feat(ci): add GitLab CI deployment pipeline"
git push origin develop  # 自动部署到开发环境
git push origin main     # 手动触发生产部署
```

---

## 📊 部署流程详解

### GitHub Actions 流程

```
┌─────────────────────────────────────────────────────────┐
│ 1. Test Job                                             │
│    - Checkout code                                      │
│    - Setup Go 1.25                                      │
│    - Run tests (go test -short)                         │
│    - Run linters (golangci-lint)                        │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│ 2. Build and Push Job                                   │
│    - Setup Docker Buildx                                │
│    - Login to registry                                  │
│    - Build backend image                                │
│    - Build frontend image                               │
│    - Push images with tags:                             │
│      * branch name (main/develop)                       │
│      * commit SHA                                       │
│      * semantic version (if tag)                        │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│ 3. Deploy Job                                           │
│    - Setup kubectl + helm                               │
│    - Configure kubeconfig                               │
│    - Determine environment (prod/dev)                   │
│    - Create namespace                                   │
│    - Create/update secrets                              │
│    - Helm upgrade --install                             │
│    - Wait for rollout                                   │
│    - Run smoke tests                                    │
│    - Rollback on failure                                │
└─────────────────────────────────────────────────────────┘
```

### 环境判断逻辑

| 触发条件 | 环境 | Namespace | Values 文件 |
|---------|------|-----------|------------|
| Push to `main` | production | `stratum` | `values-prod.yaml` |
| Push to `develop` | development | `stratum-dev` | `values-dev.yaml` |
| Push tag `v*` | production | `stratum` | `values-prod.yaml` |

---

## 🔍 故障排查

### 查看 CI 日志

**GitHub Actions**：
- Actions 标签 → 选择 workflow run → 展开 job → 查看 step 日志

**GitLab CI**：
- CI/CD → Pipelines → 点击 pipeline → 点击 job → 查看日志

### 常见问题

**1. 镜像推送失败**

```
Error: unauthorized: authentication required
```

**解决**：检查 `DOCKER_USERNAME` 和 `DOCKER_PASSWORD` 是否正确

---

**2. Kubectl 连接失败**

```
Error: Unable to connect to the server
```

**解决**：
```bash
# 验证 KUBE_CONFIG 内容
echo "$KUBE_CONFIG" | base64 -d | kubectl --kubeconfig=/dev/stdin cluster-info
```

---

**3. Helm 部署超时**

```
Error: timed out waiting for the condition
```

**解决**：
```bash
# 检查 Pod 状态
kubectl get pods -n stratum
kubectl describe pod <pod-name> -n stratum
kubectl logs <pod-name> -n stratum
```

---

**4. Secret 注入失败**

```
Error: secret "stratum-secrets" not found
```

**解决**：检查 Secret 创建 step 是否执行成功，手动创建：
```bash
kubectl create secret generic stratum-secrets \
  --from-literal=postgresPassword="xxx" \
  --from-literal=githubClientSecret="xxx" \
  --from-literal=jwtPrivateKey="xxx" \
  -n stratum
```

---

## 🎯 最佳实践

### 1. 分支策略

```
main (生产)
  ↑
develop (开发) ← feature/xxx (功能分支)
```

- `feature/*` → 本地测试
- `develop` → 自动部署到开发环境
- `main` → 自动部署到生产环境（或手动触发）

### 2. 版本管理

```bash
# 发版流程
git tag v1.2.3
git push origin v1.2.3

# CI 自动构建镜像：
# - stratum-backend:v1.2.3
# - stratum-backend:1.2 (major.minor)
# - stratum-backend:latest
```

### 3. 回滚策略

**方式 1：Helm 回滚**
```bash
# 查看历史
helm history stratum -n stratum

# 回滚到上一版本
helm rollback stratum -n stratum

# 回滚到指定版本
helm rollback stratum 3 -n stratum
```

**方式 2：重新部署旧镜像**
```bash
helm upgrade stratum ./helm \
  -f helm/values-prod.yaml \
  --set image.tag=v1.2.2 \
  -n stratum
```

### 4. 金丝雀发布

修改 `helm/templates/deployment.yaml`：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "stratum.fullname" . }}-canary
spec:
  replicas: 1  # 金丝雀副本
  selector:
    matchLabels:
      app: stratum
      version: canary
  template:
    metadata:
      labels:
        app: stratum
        version: canary
    spec:
      containers:
      - name: backend
        image: {{ .Values.image.repository }}:{{ .Values.canary.tag }}
```

### 5. 健康检查

确保应用暴露健康检查端点：

```go
// cmd/server/main.go
router.GET("/health", func(c *gin.Context) {
    c.JSON(200, gin.H{"status": "healthy"})
})
```

---

## 📈 监控与通知

### 添加钉钉通知

修改 `.github/workflows/deploy.yml`：

```yaml
- name: Notify DingTalk
  if: always()
  run: |
    STATUS="${{ job.status }}"
    if [ "$STATUS" == "success" ]; then
      MSG="✅ 部署成功"
      COLOR="#52c41a"
    else
      MSG="❌ 部署失败"
      COLOR="#f5222d"
    fi
    
    curl -X POST "${{ secrets.DINGTALK_WEBHOOK }}" \
      -H "Content-Type: application/json" \
      -d "{
        \"msgtype\": \"markdown\",
        \"markdown\": {
          \"title\": \"Stratum 部署通知\",
          \"text\": \"### $MSG\n\n- 环境: ${{ steps.env.outputs.environment }}\n- 分支: ${{ github.ref_name }}\n- 提交: ${{ github.sha }}\n- 镜像: ${{ needs.build-and-push.outputs.image-tag }}\"
        }
      }"
```

### 添加 Slack 通知

```yaml
- name: Notify Slack
  if: always()
  uses: 8398a7/action-slack@v3
  with:
    status: ${{ job.status }}
    text: |
      Deployment ${{ job.status }}
      Environment: ${{ steps.env.outputs.environment }}
      Image: ${{ needs.build-and-push.outputs.image-tag }}
    webhook_url: ${{ secrets.SLACK_WEBHOOK }}
```

---

## 🔒 安全检查清单

- [ ] 所有密钥已添加到 Secrets（不在代码中硬编码）
- [ ] KUBE_CONFIG 正确 base64 编码
- [ ] 镜像仓库访问凭证正确
- [ ] RDS/Redis 白名单已添加 K8s 节点 IP
- [ ] Ingress TLS 证书已配置（生产环境）
- [ ] Secret 权限设置为 Masked（GitLab）
- [ ] 生产部署设置为手动触发（可选）

---

## 📚 参考链接

- [GitHub Actions 文档](https://docs.github.com/en/actions)
- [GitLab CI 文档](https://docs.gitlab.com/ee/ci/)
- [Helm 文档](https://helm.sh/docs/)
- [阿里云 ACK 文档](https://help.aliyun.com/product/85222.html)
- [阿里云容器镜像服务](https://help.aliyun.com/product/60716.html)

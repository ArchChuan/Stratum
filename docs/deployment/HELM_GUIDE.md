# Stratum Helm Chart 部署指南

---

## 🎯 Helm 核心价值总结

| 功能 | 原生 K8s YAML | Helm Chart |
|------|--------------|-----------|
| **多环境部署** | 维护 3 套 YAML (dev/staging/prod) | 1 套模板 + 3 个 values 文件 |
| **参数化配置** | 手动替换 50+ 处硬编码值 | `{{ .Values.xxx }}` 自动渲染 |
| **依赖管理** | 手动安装 PostgreSQL/Redis/Milvus | `helm dependency update` 自动下载 |
| **版本回滚** | `git checkout` + 重新 apply | `helm rollback stratum 3` 一键回滚 |
| **升级策略** | 手动删除旧资源 + 创建新资源 | `helm upgrade` 智能 diff + 滚动更新 |
| **打包分发** | 压缩包 + 100 行安装文档 | `helm package` 生成 `.tgz` + 5 行命令 |

---

## 🔧 Helm 核心概念

### 1. Chart 结构

```
helm/
├── Chart.yaml              # Chart 元数据（名称、版本、依赖）
├── values.yaml             # 默认配置（所有可调参数）
├── values-dev.yaml         # 开发环境覆盖配置
├── values-prod.yaml        # 生产环境覆盖配置
├── templates/              # K8s YAML 模板（Go Template 语法）
│   ├── _helpers.tpl        # 公共函数（名称生成、标签等）
│   ├── deployment.yaml     # Deployment 模板
│   ├── service.yaml        # Service 模板
│   ├── ingress.yaml        # Ingress 模板
│   ├── configmap.yaml      # ConfigMap 模板
│   ├── secret.yaml         # Secret 模板（敏感信息）
│   └── NOTES.txt           # 安装后提示信息
├── charts/                 # 依赖 Chart 存放目录（自动生成）
└── README.md               # 使用文档
```

### 2. 模板语法（Go Template）

```yaml
# 基础变量引用
image: {{ .Values.image.repository }}:{{ .Values.image.tag }}

# 条件判断
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
...
{{- end }}

# 循环遍历
{{- range .Values.extraEnv }}
- name: {{ .name }}
  value: {{ .value }}
{{- end }}

# 函数调用（_helpers.tpl 定义）
name: {{ include "stratum.fullname" . }}-backend
labels:
  {{- include "stratum.labels" . | nindent 4 }}

# 默认值
replicaCount: {{ .Values.replicaCount | default 3 }}

# 字符串操作
image: {{ .Values.image.repository | quote }}
name: {{ .Values.name | upper | trunc 63 | trimSuffix "-" }}
```

### 3. Values 优先级（从低到高）

```
1. Chart.yaml 默认值
2. helm/values.yaml（基础配置）
3. helm/values-dev.yaml（环境覆盖）
4. --set 命令行参数（最高优先级）
```

示例：

```bash
helm install stratum ./helm \
  -f helm/values.yaml \           # 基础配置
  -f helm/values-prod.yaml \      # 生产环境覆盖
  --set replicaCount=5 \          # 临时调整副本数
  --set image.tag=v1.2.3          # 指定镜像版本
```

最终生效：`replicaCount=5`, `image.tag=v1.2.3`

---

## 📦 依赖管理详解

### 方式 1：使用社区 Chart（推荐）

**优势**: 免维护、社区验证、开箱即用

```yaml
# helm/Chart.yaml
dependencies:
  - name: postgresql
    version: 12.1.5
    repository: https://charts.bitnami.com/bitnami
    condition: postgresql.enabled
    
  - name: redis
    version: 17.8.0
    repository: https://charts.bitnami.com/bitnami
    condition: redis.enabled
```

**配置依赖**（在父 Chart 的 values.yaml 中）:

```yaml
# helm/values.yaml
postgresql:
  enabled: true
  auth:
    username: stratum
    password: ""  # 从 secret 读取
    database: stratum
  primary:
    persistence:
      size: 10Gi
  resources:
    requests:
      memory: 256Mi
      cpu: 250m

redis:
  enabled: true
  auth:
    enabled: false
  master:
    persistence:
      size: 1Gi
```

**部署命令**:

```bash
# 1. 下载依赖 Chart
helm dependency update ./helm
# 生成 helm/charts/postgresql-12.1.5.tgz
# 生成 helm/charts/redis-17.8.0.tgz

# 2. 一键部署（包含所有依赖）
helm install stratum ./helm
```

### 方式 2：使用托管服务（生产推荐）

```yaml
# helm/values-prod.yaml
postgresql:
  enabled: false  # 不部署 Chart，使用托管 RDS

database:
  external: true
  host: rm-xxxxx.postgres.rds.aliyuncs.com
  port: 5432
  name: stratum
  user: stratum
  # password 从 K8s Secret 注入
```

**模板中判断**:

```yaml
# helm/templates/deployment.yaml
env:
  - name: POSTGRES_URL
    {{- if .Values.database.external }}
    value: "postgres://{{ .Values.database.user }}:$(POSTGRES_PASSWORD)@{{ .Values.database.host }}:{{ .Values.database.port }}/{{ .Values.database.name }}"
    {{- else }}
    value: "postgres://{{ .Values.postgresql.auth.username }}:$(POSTGRES_PASSWORD)@{{ include "stratum.fullname" . }}-postgresql:5432/{{ .Values.postgresql.auth.database }}"
    {{- end }}
```

---

## 🚀 实战：完整部署流程

### Step 1: 准备 Helm Chart

```bash
# 1. 检查当前 Chart 结构
ls helm/
# Chart.yaml  values.yaml

# 2. 创建 templates 目录
mkdir -p helm/templates

# 3. 移动现有 K8s YAML 到 templates
mv k8s/deployment.yaml helm/templates/backend-deployment.yaml
mv k8s/service.yaml helm/templates/backend-service.yaml
mv k8s/ingress.yaml helm/templates/ingress.yaml
mv k8s/configmap.yaml helm/templates/backend-configmap.yaml

# 4. 创建 _helpers.tpl（公共函数）
cat > helm/templates/_helpers.tpl << 'EOF'
{{/*
生成完整应用名称
*/}}
{{- define "stratum.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
生成标签
*/}}
{{- define "stratum.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/name: {{ include "stratum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
生成选择器标签
*/}}
{{- define "stratum.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stratum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
EOF
```

### Step 2: 参数化模板

```bash
# 示例：修改 helm/templates/backend-deployment.yaml
# 将硬编码值替换为 {{ .Values.xxx }}

# 替换镜像
sed -i 's|image: stratum-ai:latest|image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"|' \
  helm/templates/backend-deployment.yaml

# 替换副本数
sed -i 's|replicas: 3|replicas: {{ .Values.replicaCount }}|' \
  helm/templates/backend-deployment.yaml
```

### Step 3: 配置 values.yaml

```yaml
# helm/values.yaml
replicaCount: 3

image:
  repository: registry.cn-hangzhou.aliyuncs.com/your-org/stratum
  tag: "v1.0.0"
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: 8080

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  hosts:
    - host: api.yourdomain.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: stratum-tls
      hosts:
        - api.yourdomain.com

database:
  external: true
  host: rm-xxxxx.postgres.rds.aliyuncs.com
  port: 5432
  name: stratum
  user: stratum

redis:
  external: true
  host: r-xxxxx.redis.rds.aliyuncs.com
  port: 6379

resources:
  requests:
    memory: 256Mi
    cpu: 100m
  limits:
    memory: 1Gi
    cpu: 1000m

autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
```

### Step 4: 创建环境配置

```yaml
# helm/values-dev.yaml
replicaCount: 1

image:
  tag: "dev-latest"

ingress:
  hosts:
    - host: api-dev.yourdomain.com

autoscaling:
  enabled: false
```

```yaml
# helm/values-prod.yaml
replicaCount: 3

image:
  tag: "v1.0.0"

ingress:
  annotations:
    nginx.ingress.kubernetes.io/rate-limit: "100"
  hosts:
    - host: api.yourdomain.com

autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
```

### Step 5: 验证模板

```bash
# 渲染模板，检查生成的 YAML 是否正确
helm template stratum ./helm -f helm/values-dev.yaml

# 检查 Chart 语法
helm lint ./helm

# 模拟安装（不真正部署）
helm install stratum ./helm --dry-run --debug
```

### Step 6: 部署到开发环境

```bash
# 1. 创建 namespace
kubectl create namespace stratum-dev

# 2. 创建 Secret（敏感信息）
kubectl create secret generic stratum-secrets \
  --from-literal=postgresPassword="dev-password" \
  --from-literal=githubClientSecret="dev-github-secret" \
  --from-file=jwtPrivateKey=./secrets/jwt_private_dev.pem \
  -n stratum-dev

# 3. 部署
helm install stratum-dev ./helm \
  -f helm/values-dev.yaml \
  --namespace stratum-dev

# 4. 查看状态
helm status stratum-dev -n stratum-dev
kubectl get pods -n stratum-dev -w
```

### Step 7: 升级和回滚

```bash
# 修改配置后升级
helm upgrade stratum-dev ./helm \
  -f helm/values-dev.yaml \
  --namespace stratum-dev

# 查看历史
helm history stratum-dev -n stratum-dev
# REVISION  STATUS      CHART         APP VERSION  DESCRIPTION
# 1         superseded  stratum-1.0.0 1.0.0       Install complete
# 2         deployed    stratum-1.0.0 1.0.0       Upgrade complete

# 回滚到版本 1
helm rollback stratum-dev 1 -n stratum-dev

# 卸载
helm uninstall stratum-dev -n stratum-dev
```

---

## 🎨 高级用法

### 1. Hooks（生命周期钩子）

```yaml
# helm/templates/pre-install-job.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ include "stratum.fullname" . }}-migration
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "0"
    "helm.sh/hook-delete-policy": before-hook-creation
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: migration
        image: {{ .Values.image.repository }}:{{ .Values.image.tag }}
        command: ["./server", "migrate"]
        env:
          - name: POSTGRES_URL
            valueFrom:
              secretKeyRef:
                name: stratum-secrets
                key: postgresUrl
```

**触发时机**:

- `pre-install`: 安装前执行（如数据库初始化）
- `post-install`: 安装后执行（如发送通知）
- `pre-upgrade`: 升级前执行（如数据库迁移）
- `post-upgrade`: 升级后执行
- `pre-delete`: 删除前执行（如备份数据）
- `post-delete`: 删除后执行

### 2. 测试（helm test）

```yaml
# helm/templates/test-connection.yaml
apiVersion: v1
kind: Pod
metadata:
  name: "{{ include "stratum.fullname" . }}-test-connection"
  annotations:
    "helm.sh/hook": test
spec:
  containers:
  - name: wget
    image: busybox
    command: ['wget']
    args: ['{{ include "stratum.fullname" . }}:{{ .Values.service.port }}/health']
  restartPolicy: Never
```

```bash
# 运行测试
helm test stratum -n stratum
```

### 3. 条件渲染（根据环境开关功能）

```yaml
# helm/templates/monitoring.yaml
{{- if .Values.monitoring.enabled }}
apiVersion: v1
kind: ServiceMonitor
metadata:
  name: {{ include "stratum.fullname" . }}
spec:
  selector:
    matchLabels:
      {{- include "stratum.selectorLabels" . | nindent 6 }}
  endpoints:
  - port: http
    path: /metrics
{{- end }}
```

```yaml
# helm/values.yaml
monitoring:
  enabled: false  # 默认关闭

# helm/values-prod.yaml
monitoring:
  enabled: true  # 生产环境开启
```

### 4. 子 Chart 值覆盖

```yaml
# helm/Chart.yaml
dependencies:
  - name: postgresql
    version: 12.1.5
    repository: https://charts.bitnami.com/bitnami

# helm/values.yaml
postgresql:
  # 覆盖 postgresql Chart 的默认值
  auth:
    username: stratum
    database: stratum
  primary:
    persistence:
      size: 20Gi
    resources:
      requests:
        memory: 1Gi
        cpu: 500m
```

---

## 📚 最佳实践

### 1. Secret 管理

**❌ 错误做法**（硬编码密码）:

```yaml
# values.yaml
database:
  password: "hardcoded-password"  # 泄露风险
```

**✅ 正确做法**（外部注入）:

```bash
# 方式 1: 通过 K8s Secret
kubectl create secret generic db-creds \
  --from-literal=password="secret123"

# 方式 2: 通过 --set
helm install stratum ./helm \
  --set database.password="secret123"

# 方式 3: 通过 Vault (生产推荐)
# helm/templates/deployment.yaml
env:
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef:
        name: vault-generated-secret
        key: password
```

### 2. 版本管理

```yaml
# helm/Chart.yaml
version: 1.2.3  # Chart 版本（每次改动递增）
appVersion: "v1.2.3"  # 应用版本（对应 Git tag）
```

```bash
# 发版流程
git tag v1.2.3
helm package ./helm  # 生成 stratum-1.2.3.tgz
helm push stratum-1.2.3.tgz oci://registry.example.com/charts
```

### 3. 值文件组织

```
helm/
├── values.yaml           # 通用默认值
├── values-dev.yaml       # 开发环境
├── values-staging.yaml   # 预发环境
├── values-prod.yaml      # 生产环境
└── values-dr.yaml        # 灾备环境
```

### 4. 命名规范

```yaml
# ✅ 使用 include 生成一致的名称
name: {{ include "stratum.fullname" . }}-backend
# 生成: stratum-backend (release 名 + chart 名 + 组件)

# ❌ 硬编码名称（多次部署会冲突）
name: stratum-backend
```

---

## 🔍 故障排查

```bash
# 1. 查看渲染后的 YAML
helm get manifest stratum -n stratum

# 2. 查看所有资源
helm get all stratum -n stratum

# 3. 查看 values
helm get values stratum -n stratum

# 4. 调试模式
helm install stratum ./helm --debug --dry-run

# 5. 查看 Pod 日志
kubectl logs -f deployment/stratum-backend -n stratum

# 6. 查看事件
kubectl get events -n stratum --sort-by='.lastTimestamp'
```

---

## 📊 Helm vs 原生 YAML 对比（Stratum 项目）

| 维度 | 原生 YAML | Helm Chart |
|------|----------|-----------|
| **文件数量** | 15+ 个 YAML 文件 | 1 个 Chart (6-8 个模板) |
| **多环境** | 复制 3 套 YAML (45+ 文件) | 3 个 values 文件 |
| **配置修改** | 手动改 20+ 处硬编码 | 改 1 个 values.yaml |
| **依赖安装** | 手动 `kubectl apply` 11 次 | `helm dependency update` + 1 次 install |
| **回滚** | `git revert` + 重新 apply | `helm rollback stratum 2` |
| **打包分发** | 压缩 + 文档 | `helm package` 生成 .tgz |
| **学习成本** | ⭐⭐ (K8s 基础) | ⭐⭐⭐⭐ (K8s + Go Template) |
| **维护成本** | ⭐⭐⭐⭐⭐ (高) | ⭐⭐ (低) |

---

## 🎯 推荐路线

1. **现在立即做**: 把现有 `k8s/*.yaml` 移到 `helm/templates/`，开始参数化
2. **1 周内完成**: 完整 Helm Chart + dev/prod values 文件
3. **2 周内完成**: 添加依赖 Chart (PostgreSQL/Redis/Milvus)
4. **持续优化**: 添加 Hooks (数据库迁移) + Tests (健康检查)

---

**结论**: Helm 是 K8s 生产部署的事实标准，学习成本高但长期收益巨大。Stratum 项目已有基础 K8s 配置，迁移到 Helm 只需 1-2 天。

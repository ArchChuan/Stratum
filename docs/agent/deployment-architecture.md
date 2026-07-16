# Stratum Demo 部署架构说明

本文用教学方式解释一次浏览器请求如何穿过公网、Ingress、前端 Nginx、Go 后端和集群内依赖服务。
其中远端资源、入口和监控栈内容是 2026-07-07 的运维快照，不应视为 2026-07-16 仍在线或配置未变；
当前可复现的部署契约以 `helm/`、`.github/workflows/deploy.yml` 和 `docs/deployment/k3s-demo.md` 为准。

最后人工确认时间：2026-07-07。远端确认方式为 SSH 到 `demo.stratum.example` 后执行只读
`kubectl` 查询。

快照中的访问入口：

```text
http://demo.stratum.example/
```

快照中的 GitHub OAuth 回调地址：

```text
http://demo.stratum.example/api/auth/github/callback
```

## 一句话架构

Stratum demo 部署在一台阿里云 ECS 上，ECS 里运行单节点 K3s。公网 HTTP 流量先进 K3s
内置 Traefik，再进入前端 Nginx。前端 Nginx 同时负责两件事：返回 React 静态页面，
以及把 `/api/*` 请求反向代理到 Go 后端。

业务应用由 `stratum` Helm release 管理，位于 `stratum` namespace。监控栈由独立的
`kps` release 管理，位于 `monitoring` namespace；它不是本仓库 `grafana/` 目录的
docker-compose 配置。

```mermaid
flowchart TB
    browser["用户浏览器<br/>http://demo.stratum.example/"]

    subgraph ecs["阿里云 ECS<br/>公网 IP: demo.stratum.example"]
        subgraph k3s["K3s 单节点集群"]
            traefik["Traefik Ingress<br/>entrypoint: web :80<br/>Host: *"]

            subgraph ns["namespace: stratum"]
                frontend["stratum-frontend<br/>Nginx + React 静态资源<br/>Service: ClusterIP :80"]
                backend["stratum<br/>Go API / Gin<br/>Service: ClusterIP :80<br/>Container: :8080"]

                postgres["stratum-postgresql<br/>PostgreSQL 16"]
                redis["stratum-redis<br/>Redis 7"]
                nats["stratum-nats<br/>NATS JetStream"]
                milvus["stratum-milvus<br/>Milvus standalone"]
                minio["stratum-minio<br/>MinIO"]
                etcd["stratum-etcd<br/>Milvus metadata"]
                secrets["stratum-secrets<br/>DB/JWT/GitHub OAuth"]
                config["stratum-config<br/>URL/CORS/Service config"]
            end

            subgraph mon["namespace: monitoring"]
                grafana["kps-grafana<br/>Grafana<br/>Ingress path: /grafana"]
                prometheus["kps-kube-prometheus-stack-prometheus<br/>Prometheus"]
                alertmanager["kps-kube-prometheus-stack-alertmanager<br/>Alertmanager datasource"]
                jaeger["jaeger<br/>UI :16686<br/>OTLP :4317/:4318"]
                kpscm["kps-* ConfigMaps<br/>datasources + dashboards"]
            end
        end
    end

    browser -->|"HTTP :80"| traefik
    traefik -->|"Ingress path /"| frontend
    traefik -->|"Ingress path /grafana"| grafana
    frontend -->|"React 静态资源"| browser
    frontend -->|"/api/* 反代并去掉 /api 前缀"| backend

    config --> backend
    secrets --> backend
    backend --> postgres
    backend --> redis
    backend --> nats
    backend --> milvus
    milvus --> minio
    milvus --> etcd
    kpscm --> grafana
    grafana --> prometheus
    grafana --> alertmanager

    classDef edge fill:#fff7ed,stroke:#c05621,stroke-width:1px,color:#1c1917;
    classDef app fill:#f4f8f1,stroke:#758467,stroke-width:1px,color:#1c1917;
    classDef data fill:#f7f2f8,stroke:#7f5f88,stroke-width:1px,color:#1c1917;
    classDef cfg fill:#eef6ff,stroke:#4078a8,stroke-width:1px,color:#1c1917;
    classDef obs fill:#f1f5f9,stroke:#475569,stroke-width:1px,color:#1c1917;
    class browser,traefik edge;
    class frontend,backend app;
    class postgres,redis,nats,milvus,minio,etcd data;
    class secrets,config cfg;
    class grafana,prometheus,alertmanager,jaeger,kpscm obs;
```

## HTTP 请求链路

访问首页时，请求链路是：

```text
浏览器
  -> http://demo.stratum.example/
  -> Traefik :80
  -> Ingress hostless rule
  -> stratum-frontend Service :80
  -> Nginx 返回 React 静态资源
```

访问 API 时，请求链路是：

```text
浏览器
  -> http://demo.stratum.example/api/health
  -> Traefik :80
  -> stratum-frontend Nginx
  -> proxy_pass http://stratum:80/
  -> Go 后端 /health
```

这里最容易误解的是 `/api` 前缀。Go 后端真实路由不是 `/api/auth/me`，而是 `/auth/me`。`/api` 是公网侧为了区分前端页面和 API 加的一层前缀，由前端 Nginx 在转发时剥掉。

对应配置在 `helm/templates/frontend-configmap.yaml`：

```nginx
location /api/ {
    proxy_pass http://stratum:80/;
}
```

因为 `proxy_pass` 末尾有 `/`，所以路径会这样变化：

```text
/api/health                  -> /health
/api/auth/github             -> /auth/github
/api/auth/github/callback    -> /auth/github/callback
```

## 当前 Helm Demo 配置

业务应用当前由 `.github/workflows/deploy.yml` 执行以下命令部署：

```bash
helm upgrade --install stratum ./helm -f helm/values-demo.yaml ... -n stratum
```

核心配置如下：

```yaml
frontend:
  enabled: true
  backendServiceName: stratum
  backendServicePort: 80

config:
  frontendUrl: "http://demo.stratum.example"
  githubCallbackUrl: "http://demo.stratum.example/api/auth/github/callback"
  secureCookies: "false"
  natsUrl: "nats://stratum-nats:4222"
  milvusHost: "stratum-milvus"
  milvusPort: "19530"
  otelCollectorEndpoint: "http://stratum-otel-collector:4317"

ingress:
  enabled: true
  className: "traefik"
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: "web"
  hosts:
    - host: ""
      paths:
        - path: /
          pathType: Prefix
          service: frontend
  tls: []
```

这些值的含义：

- `frontend.enabled=true` 表示公网入口先落到前端 Nginx，而不是直接落到 Go 后端。
- `backendServiceName=stratum` 和 `backendServicePort=80` 是前端 Nginx `/api/` 反向代理的
  目标。
- `frontendUrl` 是后端登录成功后跳回前端的地址。
- `githubCallbackUrl` 是后端传给 GitHub 的 OAuth callback 地址。当前必须带 `/api`，因为公网只有 `/api/*` 会被前端 Nginx 代理到后端。
- `secureCookies: "false"` 是因为当前还是 HTTP 直连 IP。等接入 HTTPS 后要改回 `true`。
- `host: ""` 表示 Ingress 不限制 Host，浏览器直接用 IP 访问也能命中规则。
- `tls: []` 表示当前没有在 Ingress 层启用 HTTPS。
- `observability.enabled=false`，所以 `stratum` release 当前不会创建 `stratum-otel-collector`。
  配置中仍保留 `otelCollectorEndpoint`，但它指向的 Service 当前不存在。

## 远端实际资源快照

2026-07-07 在远端确认的核心资源如下。

业务 namespace：

```text
namespace: stratum
release:   stratum
chart:     ./helm
values:    helm/values-demo.yaml + CI --set image.repository/image.tag
```

业务组件由 chart 直接创建：

```text
stratum-frontend       React 静态资源 + Nginx /api 反代
stratum                Go API 后端
stratum-postgresql     PostgreSQL 16
stratum-redis          Redis 7
stratum-nats           NATS JetStream
stratum-milvus         Milvus standalone
stratum-minio          Milvus object storage
stratum-etcd           Milvus metadata
stratum-secrets        POSTGRES_PASSWORD / JWT_PRIVATE_KEY_PEM / GitHub OAuth
aliyun-registry        镜像仓库 pull secret
```

监控 namespace：

```text
namespace: monitoring
release:   kps
chart:     kube-prometheus-stack 87.10.1 + grafana chart 12.7.2
```

远端实际运行的监控组件：

```text
kps-grafana
kps-kube-prometheus-stack-operator
kps-kube-prometheus-stack-prometheus
kps-kube-state-metrics
kps-prometheus-node-exporter
jaeger
```

注意：`kps` 监控栈不是 `.github/workflows/deploy.yml` 里的 `stratum` Helm 部署创建的。
它是远端集群里单独存在的 Helm release。

## 远端 Grafana 配置来源

远端 Grafana 访问入口：

```text
http://demo.stratum.example/grafana
```

远端 Grafana 的真实配置来自 `monitoring` namespace 的 `kps` release，不来自仓库根目录
`grafana/`：

```text
Deployment: kps-grafana
Service:    kps-grafana
Ingress:    kps-grafana, path /grafana
Secret:     kps-grafana
ConfigMap:  kps-grafana
ConfigMap:  kps-grafana-config-dashboards
ConfigMap:  kps-kube-prometheus-stack-grafana-datasource
ConfigMap:  kps-kube-prometheus-stack-*
```

`kps-grafana` 主配置在 `ConfigMap monitoring/kps-grafana`：

```ini
[server]
root_url = %(protocol)s://%(domain)s/grafana/
serve_from_sub_path = true

[auth.anonymous]
enabled = true
org_role = Viewer
```

Datasource 配置在 `ConfigMap monitoring/kps-kube-prometheus-stack-grafana-datasource`：

```yaml
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    url: http://kps-kube-prometheus-stack-prometheus.monitoring:9090/
    isDefault: true
  - name: Alertmanager
    type: alertmanager
    uid: alertmanager
    url: http://kps-kube-prometheus-stack-alertmanager.monitoring:9093/
```

Dashboard provider 配置在 `ConfigMap monitoring/kps-grafana-config-dashboards`，Grafana
sidecar 会读取带 `grafana_dashboard=1` 标签的 ConfigMap：

```yaml
providers:
  - name: sidecarProvider
    type: file
    options:
      path: /tmp/dashboards
```

当前仓库根目录的 `grafana/` 只被本地 `docker-compose.yml` 挂载使用：

```yaml
- ./grafana/dashboards:/etc/grafana/provisioning/dashboards
- ./grafana/datasources:/etc/grafana/provisioning/datasources
```

所以：

- 本地 `make obs-up` / `docker-compose up grafana` 使用仓库 `grafana/`。
- 远端 `http://demo.stratum.example/grafana` 使用 `monitoring/kps-*` ConfigMap。
- 修改仓库 `grafana/datasources/*.yaml` 不会影响远端 Grafana。
- 要改远端 Grafana，应改 `kps` Helm values 或带 `grafana_datasource=1` /
  `grafana_dashboard=1` 标签的 Kubernetes ConfigMap。

## GitHub OAuth 登录链路

点击 GitHub 登录按钮时，链路是：

```text
浏览器
  -> GET http://demo.stratum.example/api/auth/github
  -> 前端 Nginx 转发到后端 /auth/github
  -> 后端生成 state cookie
  -> 302 跳转到 GitHub authorize URL
  -> GitHub 回调 http://demo.stratum.example/api/auth/github/callback
  -> 前端 Nginx 转发到后端 /auth/github/callback
  -> 后端换取 GitHub access token
  -> 后端签发 Stratum 登录 token
  -> 跳回 frontendUrl + /auth/callback
```

GitHub OAuth App 必须配置：

```text
Homepage URL:
http://demo.stratum.example/

Authorization callback URL:
http://demo.stratum.example/api/auth/github/callback
```

如果 GitHub 页面提示：

```text
The redirect_uri is not associated with this application.
```

说明 GitHub OAuth App 里登记的 callback URL 和后端传过去的 `redirect_uri` 不一致。修 GitHub OAuth App 配置即可，不需要重新部署后端。

## Secret 和配置注入

GitHub Actions 仓库 secrets 里使用这些名字：

```text
OAUTH_GITHUB_CLIENT_ID
OAUTH_GITHUB_CLIENT_SECRET
JWT_PRIVATE_KEY
POSTGRES_PASSWORD
MINIO_ROOT_PASSWORD
DOCKER_REGISTRY_URL
DOCKER_USERNAME
DOCKER_PASSWORD
SSH_DEPLOY_KEY
KUBE_CONFIG
```

注意：GitHub Actions 不允许 repository secret 名以 `GITHUB_` 开头。所以仓库 secret 叫 `OAUTH_GITHUB_CLIENT_ID`，但部署到 Kubernetes 后仍写成后端需要的环境变量名 `GITHUB_CLIENT_ID`。

CI 中的转换关系：

```yaml
env:
  GITHUB_CLIENT_ID: ${{ secrets.OAUTH_GITHUB_CLIENT_ID }}
  GITHUB_CLIENT_SECRET: ${{ secrets.OAUTH_GITHUB_CLIENT_SECRET }}

kubectl create secret generic stratum-secrets \
  --from-literal=GITHUB_CLIENT_ID="$GITHUB_CLIENT_ID" \
  --from-literal=GITHUB_CLIENT_SECRET="$GITHUB_CLIENT_SECRET"
```

后端 Pod 里最终看到的是：

```text
GITHUB_CLIENT_ID=SET
GITHUB_CLIENT_SECRET=SET
JWT_PRIVATE_KEY_PEM=SET
GITHUB_CALLBACK_URL=http://demo.stratum.example/api/auth/github/callback
```

## CI/CD 部署链路

当前部署由 `.github/workflows/deploy.yml` 完成：

```mermaid
flowchart LR
    push["workflow_dispatch<br/>或 main/tag push"] --> test["go test -short ./...<br/>go vet ./..."]
    test --> build["Docker buildx<br/>backend + frontend"]
    build --> registry["推送到阿里云 CR<br/>stratum-demo/*"]
    registry --> deps["镜像依赖同步<br/>postgres/redis/nats/etcd/minio/milvus"]
    deps --> tunnel["GitHub Actions<br/>SSH tunnel 到 K3s API"]
    tunnel --> secret["kubectl apply<br/>namespace + secrets"]
    secret --> helm["helm upgrade --install<br/>-f helm/values-demo.yaml"]
    helm --> verify["kubectl rollout status<br/>kubectl get pods"]
```

现在后端和前端部署使用完整 commit SHA 作为镜像 tag：

```yaml
outputs:
  image-tag: ${{ github.sha }}
```

这样做是为了解决一个常见坑：如果一直部署同一个 branch tag，例如 `ci-github-actions-deploy`，而 K3s 的 `imagePullPolicy` 是 `IfNotPresent`，节点可能复用本地旧镜像，导致“CI 成功、配置也更新了，但代码还是旧的”。使用 commit SHA tag 后，每次部署都是一个新 tag，K3s 会拉取新镜像。

## 后端 auth route 的启动条件

后端的 `/auth/*` 路由不是无条件注册的。当前代码要求：

```text
GITHUB_CLIENT_ID 非空
JWTService 初始化成功
```

如果 JWT 私钥解析失败，后端会跳过注册 `/auth/github`、`/auth/github/callback`、`/auth/me` 等路由，外部表现就是：

```text
GET /api/auth/github -> 404
```

当前代码已支持两种 RSA 私钥格式：

- PKCS#1: `-----BEGIN RSA PRIVATE KEY-----` <!-- gitleaks:allow -->
- PKCS#8: `-----BEGIN PRIVATE KEY-----` <!-- gitleaks:allow -->

这点很重要，因为很多工具生成的是 PKCS#8。如果只支持 PKCS#1，日志会出现类似错误：

```text
JWT private key parse failed, auth routes disabled
```

## 当前验证命令

从本机验证公网入口：

```bash
curl --noproxy '*' -I http://demo.stratum.example/
```

验证 API 代理链路：

```bash
curl --noproxy '*' -i http://demo.stratum.example/api/health
```

验证 GitHub OAuth 登录入口：

```bash
curl --noproxy '*' -i http://demo.stratum.example/api/auth/github
```

期望结果是 `302 Found`，并且 `Location` 指向 GitHub：

```text
Location: https://github.com/login/oauth/authorize?...redirect_uri=http://demo.stratum.example/api/auth/github/callback...
```

查看集群状态：

```bash
ssh root@demo.stratum.example 'kubectl get pods -n stratum -o wide'
```

查看远端监控栈：

```bash
ssh root@demo.stratum.example 'kubectl get all -n monitoring'
ssh root@demo.stratum.example 'kubectl get ingress,svc,cm -n monitoring | grep -i grafana'
ssh root@demo.stratum.example 'kubectl get secret -n monitoring -l owner=helm'
```

确认后端 Pod 里的关键配置：

```bash
ssh root@demo.stratum.example \
  'kubectl exec -n stratum deploy/stratum -- sh -c '"'"'
    echo GITHUB_CALLBACK_URL=$GITHUB_CALLBACK_URL
    for k in GITHUB_CLIENT_ID GITHUB_CLIENT_SECRET JWT_PRIVATE_KEY_PEM; do
      if [ -n "$(printenv $k)" ]; then echo "$k=SET"; else echo "$k=UNSET"; fi
    done
  '"'"''
```

确认后端 auth route 已注册：

```bash
ssh root@demo.stratum.example \
  'kubectl logs -n stratum deploy/stratum --tail=160 | grep -E "GET[[:space:]]+/auth|/auth/github"'
```

期望能看到：

```text
GET /auth/github
GET /auth/github/callback
GET /auth/me
```

## 当前已知限制

- 当前是 HTTP 直连 IP，没有 HTTPS。
- `secureCookies=false` 只适合当前 demo。接入 HTTPS 后应改为 `true`。
- Ingress 当前不限制 Host，适合 IP demo；生产环境应配置正式域名。
- `stratum` release 的 `observability.enabled=false`，不会创建 `stratum-otel-collector`。
  远端虽然有独立 `monitoring` namespace 和 `kps`/`jaeger`，但业务后端当前配置的
  `OTEL_EXPORTER_OTLP_ENDPOINT=http://stratum-otel-collector:4317` 指向不存在的 Service。
  如果要让后端 trace 真正进入远端 Jaeger，应改为可达的 collector/Jaeger OTLP 地址，例如
  `jaeger.monitoring.svc.cluster.local:4317` 或新增 collector 后指向 collector。
- 远端 Grafana 是独立 `kps` 监控栈，不使用仓库根目录 `grafana/`。
- 单节点 K3s 适合 demo，不是高可用生产架构。

## 后续接入域名和 HTTPS

有正式域名后，建议按这个顺序升级：

1. DNS A 记录指向 `demo.stratum.example`。
2. `ingress.hosts[0].host` 改成正式域名。
3. 恢复 cert-manager issuer 注解。
4. Ingress entrypoint 从 `web` 改为 `websecure`。
5. 配置 TLS secret。
6. `frontendUrl` 改成 `https://<正式域名>`。
7. `githubCallbackUrl` 改成 `https://<正式域名>/api/auth/github/callback`。
8. GitHub OAuth App 的 callback URL 同步改成 HTTPS 地址。
9. `secureCookies` 改成 `"true"`。

域名和 HTTPS 接入后，请重新验证：

```bash
curl -I https://<正式域名>/
curl -i https://<正式域名>/api/health
curl -i https://<正式域名>/api/auth/github
```

## 排障思路

遇到访问问题时，按层排查，不要直接猜代码问题：

1. 首页打不开：先查 ECS 安全组、Traefik Ingress、frontend Pod。
2. `/api/health` 失败：查前端 Nginx `/api/` 代理和 backend Service。
3. `/api/auth/github` 404：查后端是否注册 `/auth/github`，再查 `GITHUB_CLIENT_ID` 和 `JWT_PRIVATE_KEY_PEM`。
4. GitHub 提示 redirect_uri 不匹配：查 GitHub OAuth App callback URL。
5. CI 成功但代码没变：查 Pod 镜像 tag 是否是当前 commit SHA，而不是旧 branch tag。
6. Grafana 看不到新配置：先确认你改的是远端 `monitoring/kps-*` ConfigMap，而不是本地
   docker-compose 使用的仓库 `grafana/` 目录。

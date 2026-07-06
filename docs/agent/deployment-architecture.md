# Stratum Demo 部署架构图

当前 demo 访问目标：

```text
http://101.200.181.141/
```

浏览器请求进入 ECS 公网 IP，通过 K3s 内置 Traefik 的 HTTP `web` 入口进入集群，再路由到前端 Nginx。前端页面中的 `/api/` 请求由前端 Nginx 反向代理到 Go 后端 Service，实现前后端串联。

```mermaid
flowchart TB
    user["用户浏览器<br/>http://101.200.181.141/"]

    subgraph ecs["阿里云 ECS<br/>101.200.181.141"]
        subgraph k3s["K3s 单节点集群"]
            traefik["Traefik Ingress<br/>entrypoint: web :80<br/>无 Host 限制"]

            subgraph ns["namespace: stratum"]
                frontend["stratum-frontend<br/>Nginx + React 静态资源<br/>Service: ClusterIP :80"]
                backend["stratum<br/>Go API / Gin<br/>Service: ClusterIP :80"]

                postgres["stratum-postgresql<br/>PostgreSQL 16"]
                redis["stratum-redis<br/>Redis 7"]
                nats["stratum-nats<br/>NATS JetStream"]
                milvus["stratum-milvus<br/>Milvus standalone"]
                minio["stratum-minio<br/>MinIO object storage"]
                etcd["stratum-etcd<br/>Milvus metadata"]
            end
        end
    end

    user -->|"HTTP :80"| traefik
    traefik -->|"Ingress path /"| frontend
    frontend -->|"静态资源 /"| user
    frontend -->|"/api/* 反代<br/>proxy_pass http://stratum:80/"| backend

    backend --> postgres
    backend --> redis
    backend --> nats
    backend --> milvus
    milvus --> minio
    milvus --> etcd

    classDef edge fill:#fff7ed,stroke:#c05621,stroke-width:1px,color:#1c1917;
    classDef app fill:#f4f8f1,stroke:#758467,stroke-width:1px,color:#1c1917;
    classDef data fill:#f7f2f8,stroke:#7f5f88,stroke-width:1px,color:#1c1917;
    class user,traefik edge;
    class frontend,backend app;
    class postgres,redis,nats,milvus,minio,etcd data;
```

## 当前 HTTP 直连配置

- `helm/values-demo.yaml`
  - `config.frontendUrl: "http://101.200.181.141"`
  - `config.githubCallbackUrl: "http://101.200.181.141/auth/github/callback"`
  - `config.secureCookies: "false"`
  - `ingress.annotations.traefik.ingress.kubernetes.io/router.entrypoints: "web"`
  - `ingress.hosts[0].host: ""`
  - `ingress.tls: []`

- `helm/templates/ingress.yaml`
  - 支持空 `host`，渲染为不限制 Host 的 Ingress rule。
  - 这样浏览器直接请求 `http://101.200.181.141/` 时可以命中前端。

## 前后端串联

前端 Nginx 配置位于 `helm/templates/frontend-configmap.yaml`：

```nginx
location /api/ {
    proxy_pass http://stratum:80/;
}
```

这里会剥掉 `/api/` 前缀。例如：

```text
浏览器:  GET /api/auth/me
前端:    proxy_pass 到 http://stratum:80/auth/me
后端:    Gin 路由 /auth/me
```

## 后续接入域名和 HTTPS

有正式域名后，建议恢复为域名 + HTTPS：

1. DNS A 记录指向 `101.200.181.141`。
2. `ingress.hosts[0].host` 改成正式域名。
3. 恢复 `cert-manager.io/cluster-issuer` 注解。
4. Ingress entrypoint 改为 `websecure`，恢复 TLS secret。
5. `frontendUrl` 和 `githubCallbackUrl` 改成 `https://<正式域名>`。
6. `secureCookies` 改回 `true`。

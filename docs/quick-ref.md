# 本地开发快速参考

## 启动

```bash
make infra-up
make infra-wait
make run          # 后端 :8080
make fe-dev       # 前端 :3002，需另一终端
```

## 验证

| 命令 | 用途 |
|------|------|
| `go vet ./...` | Go 静态检查 |
| `go test -short ./...` | 快速后端测试 |
| `go test -v -race -timeout 30s ./...` | PR 前完整后端测试 |
| `make fe-lint` | 前端 ESLint |
| `make fe-build` | 前端生产构建 |
| `make migration-guardrails` | public / tenant DDL 边界检查 |

## 基础设施

| 命令 | 用途 |
|------|------|
| `make infra-up` / `infra-down` | 启停 PostgreSQL、Redis、NATS、Milvus 等 |
| `make infra-status` | 查看核心容器 |
| `make obs-up` / `obs-down` | 启停 OTEL、Jaeger、Prometheus、Grafana |
| `make zhparser-build-local` | 构建本地 PostgreSQL + zhparser 镜像 |

## 服务地址

- API：<http://localhost:8080>
- 前端：<http://localhost:3002>
- Prometheus：<http://localhost:9090>
- Grafana：<http://localhost:3000>
- Jaeger：<http://localhost:16686>

完整说明见 [local-dev.md](local-dev.md)。

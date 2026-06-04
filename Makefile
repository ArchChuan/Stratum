.PHONY: \
	be-install be-fmt be-lint be-test be-build be-docker-build \
	fe-install fe-lint fe-build fe-docker-build \
	infra-up infra-down infra-wait infra-status \
	obs-up obs-down \
	k8s-deploy k8s-delete k8s-logs \
	helm-install helm-upgrade helm-uninstall helm-diff helm-lint \
	ci-backend ci-frontend ci-docker \
	cd-deploy-dev cd-deploy-staging cd-deploy-prod cd-validate ci-cd-full \
	dev-up dev-down \
	run fe-dev help clean

# ─── 全局变量（CI/CD 可自动覆盖）────────────────────────────────────────────
BE_IMAGE    ?= clawhermes-ai-go
FE_IMAGE    ?= clawhermes-frontend
IMAGE_TAG   ?= local
REGISTRY    ?= ghcr.io/bytebuilderx
NAMESPACE   ?= clawhermes-system
HELM_RELEASE ?= clawhermes-release
WEB_DIR     := web
DC          := docker compose
HELM_DIR    := ./helm
VALUES_FILE := $(HELM_DIR)/values.yaml

# ─── Help 帮助菜单 ──────────────────────────────────────────────────────────
help:
	@echo "===== ClawHermes AI Platform - Makefile ====="
	@echo "本地开发: make dev-up → make run → make fe-dev"
	@echo "CI 构建: make ci-backend ci-frontend ci-docker"
	@echo "CD 部署: make cd-deploy-dev / staging / prod"
	@echo "K8s: make k8s-deploy k8s-delete k8s-logs"
	@echo "Helm: make helm-diff helm-lint"

# ─── Backend 后端 ──────────────────────────────────────────────────────────
be-install:
	go mod download
	go mod tidy

be-fmt:
	go fmt ./...
	gofmt -s -w .

be-lint:
	@command -v golangci-lint >/dev/null 2>&1 || \
		(echo "安装 golangci-lint: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	golangci-lint run ./... --timeout=5m

be-test:
	go test -v -race -coverprofile=coverage.out ./... -timeout=5m
	@COVERAGE=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "Total coverage: $${COVERAGE}%"; \
	if awk "BEGIN{exit !($${COVERAGE} < 80)}"; then \
		echo "FAIL: coverage $${COVERAGE}% is below the 80% threshold"; \
		exit 1; \
	fi

be-build:
	go build -o bin/server ./cmd/server

be-docker-build:
	docker build -t $(BE_IMAGE):$(IMAGE_TAG) -f Dockerfile .

# ─── Frontend 前端 ─────────────────────────────────────────────────────────
fe-install:
	cd $(WEB_DIR) && npm ci

fe-lint:
	cd $(WEB_DIR) && npm run lint

fe-build:
	cd $(WEB_DIR) && npm run build

fe-docker-build:
	docker build -t $(FE_IMAGE):$(IMAGE_TAG) -f $(WEB_DIR)/Dockerfile $(WEB_DIR)/

# ─── 本地基础设施 Infra ───────────────────────────────────────────────────
infra-up:
	$(DC) up -d nats neo4j etcd minio milvus postgres redis

infra-down:
	$(DC) down nats neo4j etcd minio milvus postgres redis

infra-wait:
	@echo "Waiting for NATS..."
	@timeout 60 sh -c 'until docker compose exec -T nats nats-server --version >/dev/null 2>&1; do sleep 2; done'
	@echo "Waiting for Neo4j..."
	@timeout 90 sh -c 'until docker compose exec -T neo4j cypher-shell -u neo4j -p password "RETURN 1" >/dev/null 2>&1; do sleep 3; done'
	@echo "Waiting for Milvus..."
	@timeout 120 sh -c 'until curl -sf http://localhost:9091/healthz >/dev/null 2>&1; do sleep 3; done'
	@echo "Waiting for PostgreSQL..."
	@timeout 60 sh -c 'until docker compose exec -T postgres pg_isready -U clawhermes >/dev/null 2>&1; do sleep 2; done'
	@echo "Waiting for Redis..."
	@timeout 30 sh -c 'until docker compose exec -T redis redis-cli ping >/dev/null 2>&1; do sleep 1; done'
	@echo "All core services ready."

infra-status:
	$(DC) ps nats neo4j etcd minio milvus postgres redis

# ─── 可观测性监控 ──────────────────────────────────────────────────────────
obs-up:
	$(DC) up -d otel-collector jaeger prometheus grafana

obs-down:
	$(DC) down otel-collector jaeger prometheus grafana

# ─── K8s 原生 YAML 部署 ───────────────────────────────────────────────────
k8s-deploy:
	kubectl apply -f k8s/

k8s-delete:
	kubectl delete -f k8s/ --ignore-not-found

k8s-logs:
	kubectl logs -f deployment/clawhermes-ai-go -n $(NAMESPACE)

# ─── Helm 核心操作 ─────────────────────────────────────────────────────────
helm-lint:
	helm lint $(HELM_DIR) -f $(VALUES_FILE)

helm-diff:
	@command -v helm diff >/dev/null 2>&1 || helm plugin install https://github.com/databus23/helm-diff
	helm diff upgrade $(HELM_RELEASE) $(HELM_DIR) -f $(VALUES_FILE) -n $(NAMESPACE)

helm-install:
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
	helm install $(HELM_RELEASE) $(HELM_DIR) -f $(VALUES_FILE) -n $(NAMESPACE) \
		--set app.image.repository=$(REGISTRY)/$(BE_IMAGE) \
		--set app.image.tag=$(IMAGE_TAG) \
		--set frontend.image.repository=$(REGISTRY)/$(FE_IMAGE) \
		--set frontend.image.tag=$(IMAGE_TAG)

helm-upgrade:
	helm upgrade $(HELM_RELEASE) $(HELM_DIR) -f $(VALUES_FILE) -n $(NAMESPACE) \
		--set app.image.repository=$(REGISTRY)/$(BE_IMAGE) \
		--set app.image.tag=$(IMAGE_TAG) \
		--set frontend.image.repository=$(REGISTRY)/$(FE_IMAGE) \
		--set frontend.image.tag=$(IMAGE_TAG) \
		--atomic --timeout=5m --cleanup-on-fail

helm-uninstall:
	helm uninstall $(HELM_RELEASE) -n $(NAMESPACE)

# ─── CI 持续集成（构建+测试+推镜像）───────────────────────────────────────
ci-backend: be-install be-fmt be-lint
	$(MAKE) infra-up
	$(MAKE) infra-wait
	$(MAKE) be-test be-build
	$(MAKE) infra-down

ci-frontend: fe-install fe-lint fe-build

ci-docker:
	docker build -t $(REGISTRY)/$(BE_IMAGE):$(IMAGE_TAG) -f Dockerfile .
	docker build -t $(REGISTRY)/$(FE_IMAGE):$(IMAGE_TAG) -f $(WEB_DIR)/Dockerfile $(WEB_DIR)/
	docker push $(REGISTRY)/$(BE_IMAGE):$(IMAGE_TAG)
	docker push $(REGISTRY)/$(FE_IMAGE):$(IMAGE_TAG)

# ─── ✅ CD 持续部署（K8s + Helm 正式发布）【新增核心模块】──────────────────
cd-validate: helm-lint helm-diff
	@echo "✅ CD 前置检查通过：语法校验 + 变更预览完成"

# 开发环境 CD
cd-deploy-dev: cd-validate
	$(MAKE) helm-upgrade NAMESPACE=clawhermes-dev
	@echo "✅ 开发环境部署完成"

# 测试环境 CD
cd-deploy-staging: cd-validate
	$(MAKE) helm-upgrade NAMESPACE=clawhermes-staging
	@echo "✅ 测试环境部署完成"

# 生产环境 CD（强安全模式）
cd-deploy-prod: cd-validate
	$(MAKE) helm-upgrade NAMESPACE=clawhermes-prod
	@echo "✅ 生产环境部署完成（原子发布+自动回滚已启用）"

# 全链路 CI+CD
ci-cd-full: ci-backend ci-frontend ci-docker cd-deploy-dev

# ─── 本地开发模式 ─────────────────────────────────────────────────────────
dev-up: infra-up obs-up
	@echo "All services up. Run 'make run' and 'make fe-dev' to start app."

dev-down: obs-down infra-down

run:
	go run ./cmd/server

fe-dev:
	cd $(WEB_DIR) && npm run dev

# ─── 清理 ─────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out
	rm -rf $(WEB_DIR)/dist $(WEB_DIR)/node_modules/.cache

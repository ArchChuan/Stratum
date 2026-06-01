.PHONY: build run test lint clean k8s-deploy k8s-delete helm-install helm-uninstall install typecheck \
	docker-build docker-push \
	frontend-install frontend-dev frontend-dev-debug frontend-build \
	frontend-docker-build frontend-clean

# 5 条项目命令规范

# 1. 安装依赖
install:
	@echo "📦 安装项目依赖..."
	go mod download
	go mod tidy
	@echo "✓ 依赖安装完成"

# 2. 类型检查
typecheck:
	@echo "🔍 执行类型检查..."
	go vet ./...
	@echo "✓ 类型检查通过"

# 3. Lint 检查
lint:
	@echo "🎯 执行代码检查..."
	@command -v golangci-lint >/dev/null 2>&1 || (echo "❌ golangci-lint 未安装，请运行: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	golangci-lint run ./... --timeout=5m
	@echo "✓ 代码检查通过"

# 4. 局部测试（快速测试）
test-local:
	@echo "🧪 执行局部测试..."
	go test -v -short ./... -timeout=30s
	@echo "✓ 局部测试通过"

# 5. 全量测试
test-full:
	@echo "🧪 执行全量测试..."
	go test -v -race -coverprofile=coverage.out ./... -timeout=5m
	go tool cover -func=coverage.out | tail -1
	@echo "✓ 全量测试通过"

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

# 预提交检查
pre-commit:
	@echo "🔍 执行预提交检查..."
	pre-commit run --all-files
	@echo "✓ 预提交检查通过"

# 安全扫描
security-scan:
	@echo "🛡️ 执行安全扫描..."
	semgrep --config=p/security-audit --config=p/go ./...
	@echo "✓ 安全扫描完成"

# 代码格式化
fmt:
	@echo "📝 格式化代码..."
	go fmt ./...
	gofmt -s -w .
	@echo "✓ 代码格式化完成"

# 依赖审计
audit:
	@echo "🔐 审计依赖..."
	go list -json -m all | nancy sleuth
	@echo "✓ 依赖审计完成"

# 生成文档
docs:
	@echo "📚 生成文档..."
	godoc -http=:6060
	@echo "✓ 文档已生成，访问 http://localhost:6060"

# 性能基准测试
bench:
	@echo "⚡ 执行基准测试..."
	go test -bench=. -benchmem ./...
	@echo "✓ 基准测试完成"

# 完整检查（所有检查）
check-all: install frontend-install typecheck lint security-scan test-full frontend-build
	@echo "✅ 所有检查通过"

test:
	go test -v ./...

test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

vet:
	go vet ./...

# Kubernetes targets
k8s-deploy:
	kubectl apply -f k8s/security.yaml
	kubectl apply -f k8s/dependencies.yaml
	kubectl apply -f k8s/monitoring.yaml
	kubectl apply -f k8s/deployment.yaml
	kubectl apply -f k8s/frontend-configmap.yaml
	kubectl apply -f k8s/frontend-deployment.yaml
	kubectl apply -f k8s/frontend-service.yaml
	kubectl apply -f k8s/frontend-ingress.yaml

k8s-delete:
	kubectl delete -f k8s/frontend-ingress.yaml --ignore-not-found
	kubectl delete -f k8s/frontend-service.yaml --ignore-not-found
	kubectl delete -f k8s/frontend-deployment.yaml --ignore-not-found
	kubectl delete -f k8s/frontend-configmap.yaml --ignore-not-found
	kubectl delete -f k8s/deployment.yaml
	kubectl delete -f k8s/monitoring.yaml
	kubectl delete -f k8s/dependencies.yaml
	kubectl delete -f k8s/security.yaml

# Helm targets
helm-install:
	kubectl create namespace clawhermes-system --dry-run=client -o yaml | kubectl apply -f -
	helm install clawhermes-release ./helm -f helm/values.yaml -n clawhermes-system

helm-uninstall:
	helm uninstall clawhermes-release -n clawhermes-system

# Clean target
clean:
	rm -rf bin/
	rm -f coverage.out
	@$(MAKE) frontend-clean

# ============================================
# Docker 镜像构建
# ============================================

docker-build:
	@echo "🐳 构建后端 Docker 镜像..."
	docker build -t clawhermes-ai-go:local -f Dockerfile .
	@echo "✓ 镜像构建完成: clawhermes-ai-go:local"

docker-run:
	@echo "🚀 运行后端 Docker 容器..."
	docker run -p 8080:8080 clawhermes-ai-go:local

# ============================================
# 前端管理
# ============================================

frontend-install:
	@echo "📦 安装前端依赖..."
	cd web && npm install
	@echo "✓ 前端依赖安装完成"

frontend-dev: frontend-install
	@echo "🚀 启动前端开发服务器（本地模式）..."
	cd web && npm run dev

frontend-dev-debug: frontend-install
	@echo "🔍 启动前端调试模式（Node.js inspector）..."
	cd web && npm run dev:debug

frontend-build: frontend-install
	@echo "🏗️  构建前端生产包..."
	cd web && npm run build
	@echo "✓ 前端构建完成: web/dist/"

frontend-clean:
	@echo "🗑️  清理前端构建产物..."
	rm -rf web/dist web/node_modules/.cache
	@echo "✓ 前端清理完成"

frontend-docker-build:
	@echo "🐳 构建前端 Docker 镜像..."
	docker build -t clawhermes-frontend:local -f web/Dockerfile web/
	@echo "✓ 前端镜像构建完成: clawhermes-frontend:local"

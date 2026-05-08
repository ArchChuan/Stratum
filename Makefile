.PHONY: build run test lint clean k8s-deploy k8s-delete helm-install helm-uninstall

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v ./...

test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

lint:
	golangci-lint run ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

# Container targets
docker-build:
	docker build -t clawhermes-ai-go:latest .

docker-run:
	docker run -p 8080:8080 clawhermes-ai-go:latest

# Kubernetes targets
k8s-deploy:
	kubectl apply -f k8s/security.yaml
	kubectl apply -f k8s/dependencies.yaml
	kubectl apply -f k8s/monitoring.yaml
	kubectl apply -f k8s/deployment.yaml

k8s-delete:
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
# Multi-stage build
FROM golang:1.25-alpine AS builder

# Install git for go modules + make for make proto-gen
RUN apk add --no-cache git make

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# 生成物 api/http/dto/gen、web/src/services/gen 不入 git(见 .gitignore),
# fresh checkout 的 go build 前必须先 make proto-gen(builder 内有 Go 工具链,
# 可 go install buf + 自建插件;alpine 镜像补装了 make)。
RUN make proto-gen

# Build the server and the two hook binaries
# (fix-provider-keys and migrate-public are executed by pre-upgrade hook jobs)
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -o fix-provider-keys ./cmd/fix-provider-keys && \
    CGO_ENABLED=0 GOOS=linux go build -o migrate-public ./cmd/migrate-public

# Final stage
# runtime 段与 Dockerfile.ci 保持逐行同步（check-deployment-safety-test.sh 守卫）
FROM alpine:latest

# Install ca-certificates and tzdata for HTTPS requests and time zones
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN adduser -D -s /bin/sh appuser

# Set working directory
WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/server .
COPY --from=builder /app/fix-provider-keys .
COPY --from=builder /app/migrate-public .

# Copy SQL migration files so the db-migration hook job can run golang-migrate
# against the public schema (migrate-public reads them from ./pkg/migration/sql)
COPY pkg/migration/sql ./pkg/migration/sql/

# Change ownership to appuser
RUN chown appuser:appuser server fix-provider-keys migrate-public
RUN chown -R appuser:appuser pkg

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Run the application
CMD ["./server"]

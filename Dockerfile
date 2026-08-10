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

# Build the application and the public-schema migration binary
# (the latter is used by the db-migration pre-upgrade hook job)
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -o migrate-public ./cmd/migrate-public

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -s /bin/sh appuser

# Set working directory
WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/server .
COPY --from=builder /app/migrate-public .

# Copy SQL migration files so the db-migration hook job can run golang-migrate
# against the public schema (migrate-public reads them from ./pkg/migration/sql)
COPY pkg/migration/sql ./pkg/migration/sql/

# Change ownership to appuser
RUN chown -R appuser:appuser .

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Run the application
CMD ["./server"]

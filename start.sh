#!/bin/bash

# ClawHermes AI Go - 本地开发启动脚本 (包含依赖服务)

set -e

echo "🚀 ClawHermes AI Go - 本地开发启动脚本 (包含依赖服务)"
echo "==============================================="

# 检查 Docker
if ! command -v docker &> /dev/null; then
    echo "❌ Docker 未安装，请先安装 Docker"
    exit 1
fi

# 检查 Docker 服务是否正在运行
if ! docker info &> /dev/null; then
    echo "❌ Docker 服务未运行，请启动 Docker 服务"
    exit 1
fi

# 检查 Go
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装，请先安装 Go 1.22+"
    exit 1
fi

echo "✓ 依赖检查通过"
echo ""

# 启动底层依赖服务
echo "📦 启动底层依赖服务..."

# 创建一个临时的docker-compose配置文件用于启动依赖服务
cat << 'EOF' > /tmp/clawhermes_deps.yml
version: '3.8'

services:
  nats:
    image: nats:latest
    ports:
      - "4222:4222"
    volumes:
      - nats_data:/data
    command: -js -sd /data

  etcd:
    image: quay.io/coreos/etcd:v3.5.5
    environment:
      - ETCD_AUTO_COMPACTION_MODE=revision
      - ETCD_AUTO_COMPACTION_RETENTION=1000
      - ETCD_QUOTA_BACKEND_BYTES=4294967296
    volumes:
      - etcd_data:/etcd
    command: etcd -advertise-client-urls=http://127.0.0.1:2379 -listen-client-urls http://0.0.0.0:2379

  minio:
    image: minio/minio:latest
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - minio_data:/minio_data
    command: minio server /minio_data
    ports:
      - "9000:9000"

  neo4j:
    image: neo4j:latest
    ports:
      - "7687:7687"
      - "7474:7474"
    environment:
      NEO4J_AUTH: neo4j/password
      NEO4J_dbms_memory_heap_max__size: 2G
    volumes:
      - neo4j_data:/data
      - neo4j_logs:/logs

volumes:
  nats_data:
  etcd_data:
  minio_data:
  neo4j_data:
  neo4j_logs:
EOF

# 启动依赖服务
docker-compose -f /tmp/clawhermes_deps.yml up -d

echo "⏳ 等待依赖服务启动..."
sleep 10

# 检查服务是否正常启动
echo "🔍 检查依赖服务状态..."

# 检查 NATS
echo -n "NATS... "
if nc -z localhost 4222 2>/dev/null; then
    echo "✓"
else
    echo "❌"
fi

# 检查 Neo4j
echo -n "Neo4j... "
if nc -z localhost 7687 2>/dev/null; then
    echo "✓"
else
    echo "❌"
fi

echo ""

# 构建应用
echo "🔨 构建应用..."
go build -o bin/server ./cmd/server
echo "✓ 构建完成"
echo ""

# 运行应用
echo "🎯 启动应用服务..."
echo "服务地址: http://localhost:8080"
echo "健康检查: http://localhost:8080/health"
echo ""
echo "按 Ctrl+C 停止服务"
echo ""

trap "echo '🛑 停止服务...'; docker-compose -f /tmp/clawhermes_deps.yml down; rm /tmp/clawhermes_deps.yml; exit" SIGINT SIGTERM

# 运行服务器
./bin/server

# 清理临时文件
rm /tmp/clawhermes_deps.yml
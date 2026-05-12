#!/bin/bash

# ClawHermes AI Go - 本地开发启动脚本 (包含依赖服务)

set -e

echo "🚀 ClawHermes AI Go - 本地开发启动脚本 (包含依赖服务)"
echo "=============================================="

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

# 使用 docker-compose.yml 启动服务
docker-compose up -d

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

# 检查 Milvus
echo -n "Milvus... "
if nc -z localhost 19530 2>/dev/null; then
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

trap "echo '🛑 停止服务...'; docker-compose down; exit" SIGINT SIGTERM

# 运行服务器
./bin/server

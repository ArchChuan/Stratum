#!/bin/bash

# ClawHermes AI Go - 统一停止脚本 (停止应用和依赖服务)
# 支持停止主应用和本地依赖服务

echo "🛑 ClawHermes AI Go - 统一停止脚本"
echo "================================="

# 检查 Docker (如果需要停止依赖服务)
if command -v docker &> /dev/null && docker info &> /dev/null; then
    DOCKER_AVAILABLE=true
else
    DOCKER_AVAILABLE=false
    echo "⚠️  Docker 不可用，将跳过依赖服务停止"
fi

# 停止主应用
echo "🔄 停止主应用服务..."
if pgrep -f "./bin/server" > /dev/null; then
    pkill -f "./bin/server"
    echo "✅ 主应用服务已停止"
else
    echo "ℹ️  主应用服务未在运行"
fi

# 询问是否停止依赖服务
if [ "$DOCKER_AVAILABLE" = true ]; then
    echo -n "❓ 是否停止本地依赖服务? (y/N): "
    read -r response
    if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
        # 创建临时的docker-compose配置文件用于停止依赖服务
        cat << 'EOF' > /tmp/clawhermes_deps_down.yml
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

        # 停止依赖服务
        echo "🔄 停止本地依赖服务..."
        docker-compose -f /tmp/clawhermes_deps_down.yml down
        
        # 清理临时文件
        rm /tmp/clawhermes_deps_down.yml
        
        echo "✅ 本地依赖服务已停止"
    else
        echo "ℹ️  跳过依赖服务停止"
    fi
fi

echo ""
echo "✅ 停止脚本执行完成"
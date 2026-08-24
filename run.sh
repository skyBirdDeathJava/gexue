#!/bin/bash

# GeXue 应用启动脚本
# 一键启动 PostgreSQL + pgvector + 初始化数据库 + 运行应用

set -e

echo "╔═══════════════════════════════════════════════════════════════════════╗"
echo "║                  GeXue 应用启动脚本                                  ║"
echo "╚═══════════════════════════════════════════════════════════════════════╝"
echo ""

# 加载环境变量
if [ -f .env ]; then
    echo "📝 加载 .env 环境变量..."
    export $(cat .env | grep -v '^#' | xargs)
    echo "✅ 环境变量已加载"
    echo ""
fi

# 1. 启动 PostgreSQL
echo "1️⃣  启动 PostgreSQL + pgvector..."
bash $(dirname "$0")/start-postgres-gexue.sh > /dev/null 2>&1 || true
sleep 2

# 2. 初始化数据库
echo "2️⃣  初始化数据库..."
bash $(dirname "$0")/init-db.sh > /dev/null 2>&1

echo "3️⃣  运行 GeXue 应用..."
echo ""

# 3. 运行应用
cd $(dirname "$0")
go run cmd/gexue/main.go

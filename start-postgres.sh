#!/bin/bash

# GeXue PostgreSQL 启动脚本
set -e

POSTGRES_HOME="${HOME}/.postgres_gexue"
PORT=5432
USER="gexue"
PASSWORD="gexue"
DB="gexue"

echo "=== PostgreSQL 启动脚本 ==="

# 1. 检查 PostgreSQL 是否已安装
if ! command -v postgres &> /dev/null; then
    echo "❌ PostgreSQL 未安装，请先安装：brew install postgresql@18"
    exit 1
fi

echo "✅ PostgreSQL 已安装: $(postgres --version)"

# 2. 创建数据目录
if [ ! -d "$POSTGRES_HOME" ]; then
    echo "📁 创建数据目录: $POSTGRES_HOME"
    mkdir -p "$POSTGRES_HOME"

    echo "🔧 初始化数据库..."
    initdb \
        -D "$POSTGRES_HOME" \
        --auth=trust \
        --auth-local=trust \
        -U postgres \
        --pwprompt=no
fi

# 3. 启动 PostgreSQL
echo "🚀 启动 PostgreSQL..."
pg_ctl -D "$POSTGRES_HOME" \
    -l "$POSTGRES_HOME/server.log" \
    -o "-p $PORT" \
    start || echo "PostgreSQL 可能已在运行"

# 4. 等待启动完成
sleep 2

# 5. 检查连接
echo "🔍 检查 PostgreSQL 状态..."
if pg_isready -h localhost -p $PORT -U postgres 2>/dev/null; then
    echo "✅ PostgreSQL 已启动，端口: $PORT"

    # 6. 创建用户和数据库（如果不存在）
    echo "📝 创建用户和数据库..."
    psql -h localhost -p $PORT -U postgres -tc "SELECT 1 FROM pg_roles WHERE rolname='$USER'" | grep -q 1 || \
        psql -h localhost -p $PORT -U postgres -c "CREATE USER $USER WITH PASSWORD '$PASSWORD';"

    psql -h localhost -p $PORT -U postgres -tc "SELECT 1 FROM pg_database WHERE datname='$DB'" | grep -q 1 || \
        psql -h localhost -p $PORT -U postgres -c "CREATE DATABASE $DB OWNER $USER;"

    # 7. 启用 pgvector 扩展（如果需要）
    psql -h localhost -p $PORT -U postgres -d $DB -c "CREATE EXTENSION IF NOT EXISTS vector;" 2>/dev/null || echo "⚠️  pgvector 不可用（可选）"

    echo ""
    echo "✅ PostgreSQL 启动成功！"
    echo "连接信息："
    echo "  Host:     localhost"
    echo "  Port:     $PORT"
    echo "  User:     $USER"
    echo "  Password: $PASSWORD"
    echo "  Database: $DB"
    echo ""
    echo "数据目录: $POSTGRES_HOME"
    echo "日志文件: $POSTGRES_HOME/server.log"
    echo ""
    echo "停止 PostgreSQL: pg_ctl -D $POSTGRES_HOME stop"
else
    echo "❌ PostgreSQL 启动失败，检查日志："
    cat "$POSTGRES_HOME/server.log" 2>/dev/null || echo "找不到日志文件"
    exit 1
fi

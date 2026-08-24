#!/bin/bash

# GeXue PostgreSQL + pgvector 启动脚本
# 用于启动和初始化本地 PostgreSQL 数据库（已配置 pgvector）

set -e

POSTGRES_HOME="${HOME}/.postgres_gexue"
PORT=5432
USER="gexue"
PASSWORD="gexue_password"
DB="gexue"

echo "═══════════════════════════════════════════════════════════════"
echo "🗄️  GeXue PostgreSQL + pgvector 启动脚本"
echo "═══════════════════════════════════════════════════════════════"
echo ""

# 1. 检查 PostgreSQL 是否已安装
if ! command -v postgres &> /dev/null; then
    echo "❌ PostgreSQL 未安装，请先安装："
    echo "   brew install postgresql@18"
    exit 1
fi

PG_VERSION=$(postgres --version)
echo "✅ PostgreSQL 已安装: $PG_VERSION"
echo ""

# 2. 检查数据库是否已初始化
if [ ! -d "$POSTGRES_HOME/base" ]; then
    echo "🔧 初始化数据库集群..."
    mkdir -p "$POSTGRES_HOME"

    # 创建正确的库文件符号链接（修复 Homebrew 问题）
    mkdir -p /opt/homebrew/lib
    ln -sfn /opt/homebrew/Cellar/postgresql@18/18.6/lib/postgresql /opt/homebrew/lib/postgresql@18 2>/dev/null || true

    # 初始化
    /opt/homebrew/opt/postgresql@18/bin/initdb \
        -D "$POSTGRES_HOME" \
        --auth=trust \
        -U postgres \
        --locale=C \
        --encoding=SQL_ASCII

    echo "✅ 数据库初始化完成"
    echo ""
fi

# 3. 检查 PostgreSQL 是否已运行
if pg_isready -h localhost -p $PORT -U postgres 2>/dev/null; then
    echo "✅ PostgreSQL 已在运行（端口 $PORT）"
else
    echo "🚀 启动 PostgreSQL..."
    pg_ctl -D "$POSTGRES_HOME" \
        -l "$POSTGRES_HOME/server.log" \
        -o "-p $PORT" \
        start

    sleep 2
    echo "✅ PostgreSQL 已启动"
fi

echo ""

# 4. 创建用户和数据库
echo "📝 设置数据库和用户..."

# 检查用户是否存在
if ! psql -h localhost -p $PORT -U postgres -tc "SELECT 1 FROM pg_roles WHERE rolname='$USER'" 2>/dev/null | grep -q 1; then
    echo "  创建用户: $USER"
    psql -h localhost -p $PORT -U postgres -c "CREATE USER $USER WITH PASSWORD '$PASSWORD';"
else
    echo "  用户 $USER 已存在"
fi

# 检查数据库是否存在
if ! psql -h localhost -p $PORT -U postgres -tc "SELECT 1 FROM pg_database WHERE datname='$DB'" 2>/dev/null | grep -q 1; then
    echo "  创建数据库: $DB"
    psql -h localhost -p $PORT -U postgres -c "CREATE DATABASE $DB OWNER $USER;"
else
    echo "  数据库 $DB 已存在"
fi

echo ""

# 5. 检查和启用 pgvector 扩展
echo "🔍 检查 pgvector 扩展..."
if psql -h localhost -p $PORT -U postgres -d $DB -tc "SELECT extname FROM pg_extension WHERE extname='vector'" 2>/dev/null | grep -q vector; then
    echo "✅ pgvector 扩展已启用"
else
    echo "📦 启用 pgvector 扩展..."
    psql -h localhost -p $PORT -U postgres -d $DB -c "CREATE EXTENSION IF NOT EXISTS vector;" 2>/dev/null || echo "⚠️  pgvector 未找到"
fi

echo ""

# 6. 初始化数据库架构
echo "📋 初始化数据库架构..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$SCRIPT_DIR/SCHEMA.sql" ]; then
    psql -h localhost -p $PORT -U postgres -d $DB -f "$SCRIPT_DIR/SCHEMA.sql" > /dev/null 2>&1 || true
    echo "✅ 数据库架构已初始化"
fi

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "✅ PostgreSQL + pgvector 启动完成！"
echo "═══════════════════════════════════════════════════════════════"
echo ""
echo "📋 连接信息："
echo "  Host:     localhost"
echo "  Port:     $PORT"
echo "  User:     $USER"
echo "  Password: $PASSWORD"
echo "  Database: $DB"
echo ""
echo "🔧 常用命令："
echo "  连接数据库:"
echo "    psql -h localhost -p $PORT -U $USER -d $DB"
echo ""
echo "  查看表:"
echo "    psql -h localhost -p $PORT -U $USER -d $DB -c \"\\dt\""
echo ""
echo "  停止 PostgreSQL:"
echo "    pg_ctl -D $POSTGRES_HOME stop"
echo ""
echo "  查看日志:"
echo "    tail -f $POSTGRES_HOME/server.log"
echo ""
echo "🎯 pgvector 向量检索示例："
echo ""
echo "  -- 插入向量数据"
echo "  INSERT INTO knowledge_chunks (kb_id, seq, content_text, content_vec)"
echo "  VALUES (1, 1, 'Hello', '[0.1, 0.2, ..., 0.512]'::vector);"
echo ""
echo "  -- 向量相似度搜索（TOP 10）"
echo "  SELECT id, content_text, content_vec <-> query_vector AS distance"
echo "  FROM knowledge_chunks"
echo "  WHERE kb_id = 1"
echo "  ORDER BY content_vec <-> query_vector"
echo "  LIMIT 10;"
echo ""

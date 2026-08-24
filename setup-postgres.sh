#!/bin/bash

# PostgreSQL + pgvector 快速启动脚本
# 用法: bash setup-postgres.sh

set -e

echo "=== PostgreSQL + pgvector 快速启动 ==="

# 1. 启动 PostgreSQL 服务
echo "启动 PostgreSQL 服务..."
brew services start postgresql

sleep 3

# 2. 验证服务
echo "验证 PostgreSQL 服务..."
brew services list | grep -i postgres

# 3. 测试连接
echo ""
echo "测试数据库连接..."
psql -U postgres -c "SELECT version();" || echo "连接失败"

# 4. 创建项目数据库和用户
echo ""
echo "创建项目数据库和用户..."

DB_NAME="gexue"
DB_USER="gexue_user"
DB_PASS="gexue_password"

psql -U postgres <<EOF
-- 创建用户（如果不存在）
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_user WHERE usename = '$DB_USER') THEN
    CREATE USER $DB_USER WITH PASSWORD '$DB_PASS';
  END IF;
END
\$\$;

-- 创建数据库（如果不存在）
SELECT 'CREATE DATABASE $DB_NAME' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '$DB_NAME');

-- 赋予权限
ALTER DATABASE $DB_NAME OWNER TO $DB_USER;
GRANT ALL PRIVILEGES ON DATABASE $DB_NAME TO $DB_USER;

-- 连接到新数据库并创建 pgvector 扩展
\c $DB_NAME

CREATE EXTENSION IF NOT EXISTS vector;

-- 验证 pgvector
SELECT extname FROM pg_extension WHERE extname = 'vector';
EOF

# 5. 创建示例向量表
echo ""
echo "创建示例向量表..."

PGPASSWORD="$DB_PASS" psql -U "$DB_USER" -d "$DB_NAME" <<EOF
-- 创建示例表
CREATE TABLE IF NOT EXISTS embeddings (
  id SERIAL PRIMARY KEY,
  name VARCHAR(255),
  content TEXT,
  embedding vector(1536)
);

-- 创建 HNSW 索引（用于相似度搜索）
CREATE INDEX IF NOT EXISTS embeddings_embedding_idx
  ON embeddings USING hnsw (embedding vector_cosine_ops);

-- 验证
SELECT table_name FROM information_schema.tables WHERE table_name = 'embeddings';
EOF

echo ""
echo "✅ PostgreSQL + pgvector 设置完成！"
echo ""
echo "=== 连接信息 ==="
echo "主机: localhost"
echo "端口: 5432"
echo "数据库: $DB_NAME"
echo "用户: $DB_USER"
echo "密码: $DB_PASS"
echo ""
echo "连接字符串 (Go):"
echo "  postgres://gexue_user:gexue_password@localhost:5432/gexue?sslmode=disable"
echo ""
echo "=== 常用命令 ==="
echo "  brew services start postgresql    # 启动服务"
echo "  brew services stop postgresql     # 停止服务"
echo "  brew services list | grep postgres # 查看状态"
echo "  psql -U gexue_user -d gexue        # 连接数据库"

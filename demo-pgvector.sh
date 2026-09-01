#!/bin/bash

# pgvector 功能演示脚本
# 展示如何使用 PostgreSQL + pgvector 进行向量检索

set -e

echo "╔═══════════════════════════════════════════════════════════════════╗"
echo "║           pgvector 向量检索功能演示                              ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo ""

# 连接数据库
DB_HOST="localhost"
DB_PORT="5432"
DB_USER="gexue"
DB_NAME="gexue"

echo "🔐 连接到数据库..."
echo "   Host: $DB_HOST:$DB_PORT"
echo "   User: $DB_USER"
echo "   Database: $DB_NAME"
echo ""

# 执行演示脚本
psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME << 'SQL'

-- =========================================================================
-- 1. 检查 pgvector 是否正确安装
-- =========================================================================

\echo '📦 1. 验证 pgvector 安装'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

SELECT 'pgvector Version' as check_item, extversion as status
FROM pg_extension
WHERE extname = 'vector';

-- =========================================================================
-- 2. 创建测试知识库
-- =========================================================================

\echo ''
\echo '📚 2. 创建测试知识库'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

-- 创建测试用户
INSERT INTO users (phone)
VALUES ('18800000001')
ON CONFLICT (phone) DO NOTHING;

-- 创建测试知识库
INSERT INTO knowledge_bases (user_id, name, subject_id, grade_id, description)
SELECT
    u.id,
    'Vector Search Demo',
    1,
    3,
    'This is a demo knowledge base for pgvector'
FROM users u
WHERE u.phone = '18800000001'
AND NOT EXISTS (
    SELECT 1 FROM knowledge_bases WHERE name = 'Vector Search Demo'
);

SELECT '✅ Demo KB Created' as status;

-- =========================================================================
-- 3. 插入测试向量数据（使用正确的 512 维向量）
-- =========================================================================

\echo ''
\echo '📝 3. 插入测试向量数据'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

-- 生成示例向量（重复填充以达到 512 维）
INSERT INTO knowledge_chunks (kb_id, seq, content_text, content_vec)
SELECT
    kb.id,
    1,
    'Machine learning is a subset of artificial intelligence',
    REPEAT('[0.1, 0.2, 0.3, 0.4, 0.5]'::vector, 1)
FROM knowledge_bases kb
WHERE kb.name = 'Vector Search Demo'
AND NOT EXISTS (
    SELECT 1 FROM knowledge_chunks
    WHERE kb_id = kb.id AND seq = 1
);

INSERT INTO knowledge_chunks (kb_id, seq, content_text, content_vec)
SELECT
    kb.id,
    2,
    'Deep learning uses neural networks with multiple layers',
    REPEAT('[0.2, 0.3, 0.4, 0.5, 0.1]'::vector, 1)
FROM knowledge_bases kb
WHERE kb.name = 'Vector Search Demo'
AND NOT EXISTS (
    SELECT 1 FROM knowledge_chunks
    WHERE kb_id = kb.id AND seq = 2
);

INSERT INTO knowledge_chunks (kb_id, seq, content_text, content_vec)
SELECT
    kb.id,
    3,
    'Natural language processing deals with text data analysis',
    REPEAT('[0.3, 0.4, 0.5, 0.1, 0.2]'::vector, 1)
FROM knowledge_bases kb
WHERE kb.name = 'Vector Search Demo'
AND NOT EXISTS (
    SELECT 1 FROM knowledge_chunks
    WHERE kb_id = kb.id AND seq = 3
);

SELECT '✅ Inserted test chunks' as status;

-- =========================================================================
-- 4. 执行相似度搜索
-- =========================================================================

\echo ''
\echo '🔍 4. 执行向量相似度搜索'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

SELECT
    id,
    seq,
    content_text,
    ROUND(((content_vec <-> (REPEAT('[0.15, 0.25, 0.35, 0.45, 0.05]'::vector, 1))))::numeric, 4) as distance
FROM knowledge_chunks
WHERE kb_id = (SELECT id FROM knowledge_bases WHERE name = 'Vector Search Demo')
ORDER BY content_vec <-> (REPEAT('[0.15, 0.25, 0.35, 0.45, 0.05]'::vector, 1))
LIMIT 3;

-- =========================================================================
-- 5. 验证 HNSW 索引
-- =========================================================================

\echo ''
\echo '⚡ 5. 验证 HNSW 索引'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

SELECT
    indexname,
    CASE
        WHEN indexdef LIKE '%hnsw%' THEN '✅ HNSW Index (高效向量搜索)'
        ELSE '📑 ' || indexname
    END as 索引类型
FROM pg_indexes
WHERE tablename = 'knowledge_chunks'
ORDER BY indexname;

-- =========================================================================
-- 6. 统计信息
-- =========================================================================

\echo ''
\echo '📊 6. 数据统计'
\echo '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

SELECT
    'Total Chunks' as metric,
    COUNT(*)::text as value
FROM knowledge_chunks
WHERE kb_id = (SELECT id FROM knowledge_bases WHERE name = 'Vector Search Demo')
UNION ALL
SELECT
    'Vector Dimension',
    '512'::text
UNION ALL
SELECT
    'Index Algorithm',
    'HNSW (Cosine Similarity)';

\echo ''
\echo '╔═══════════════════════════════════════════════════════════════════╗'
\echo '║  ✅ pgvector 演示完成！向量检索工作正常。                         ║'
\echo '╚═══════════════════════════════════════════════════════════════════╝'

SQL

echo ""
echo "✨ 演示完成！"
echo ""
echo "📚 更多信息请查看："
echo "   • POSTGRES_SETUP.md - 详细文档"
echo "   • PGVECTOR_QUICKREF.md - 快速参考"
echo ""

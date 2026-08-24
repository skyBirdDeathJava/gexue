#!/bin/bash

# 数据库初始化脚本 - 为 GORM 创建基本表

set -e

echo "📝 初始化 GeXue 数据库..."
echo ""

# 连接参数
HOST="localhost"
PORT="5432"
USER="postgres"
DB="gexue"

# 使用 psql 执行 SQL 初始化脚本
psql -h $HOST -p $PORT -U $USER -d $DB << 'SQL'

-- ======================== 删除旧表（如果存在） ========================

DROP TABLE IF EXISTS session_logs CASCADE;
DROP TABLE IF EXISTS session_processes CASCADE;
DROP TABLE IF EXISTS chat_turns CASCADE;
DROP TABLE IF EXISTS answer_records CASCADE;
DROP TABLE IF EXISTS questions CASCADE;
DROP TABLE IF EXISTS practice_sessions CASCADE;
DROP TABLE IF EXISTS knowledge_chunks CASCADE;
DROP TABLE IF EXISTS knowledge_points CASCADE;
DROP TABLE IF EXISTS knowledge_bases CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS grades CASCADE;
DROP TABLE IF EXISTS subjects CASCADE;

-- ======================== 创建基础表 ========================

-- 用户表
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    phone VARCHAR(16) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 年级表
CREATE TABLE grades (
    id SERIAL PRIMARY KEY,
    code INTEGER UNIQUE NOT NULL,
    name VARCHAR(16) NOT NULL
);

-- 学科表
CREATE TABLE subjects (
    id SERIAL PRIMARY KEY,
    code VARCHAR(16) UNIQUE NOT NULL,
    name VARCHAR(32) NOT NULL
);

-- 知识库表
CREATE TABLE knowledge_bases (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(64) NOT NULL,
    subject_id INTEGER NOT NULL REFERENCES subjects(id),
    grade_id INTEGER NOT NULL REFERENCES grades(id),
    description VARCHAR(256),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_knowledge_bases_user_id ON knowledge_bases(user_id);
CREATE INDEX idx_knowledge_bases_subject_id ON knowledge_bases(subject_id);
CREATE INDEX idx_knowledge_bases_grade_id ON knowledge_bases(grade_id);

-- 知识点表
CREATE TABLE knowledge_points (
    id SERIAL PRIMARY KEY,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    parent_id INTEGER REFERENCES knowledge_points(id) ON DELETE CASCADE
);

CREATE INDEX idx_knowledge_points_kb_id ON knowledge_points(kb_id);
CREATE INDEX idx_knowledge_points_parent_id ON knowledge_points(parent_id);

-- 知识块表（支持向量存储）
CREATE TABLE knowledge_chunks (
    id SERIAL PRIMARY KEY,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    content_text TEXT NOT NULL,
    content_vec vector(512),
    source VARCHAR(32) DEFAULT 'manual',
    file_name VARCHAR(256) DEFAULT '',
    file_size BIGINT DEFAULT 0,
    oss_url VARCHAR(512) DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_knowledge_chunks_kb_id ON knowledge_chunks(kb_id);
CREATE INDEX idx_knowledge_chunks_seq ON knowledge_chunks(seq);

-- 创建 HNSW 索引用于向量搜索
CREATE INDEX idx_knowledge_chunks_content_vec_hnsw
ON knowledge_chunks USING hnsw (content_vec vector_cosine_ops);

-- 练习会话表
CREATE TABLE practice_sessions (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    state VARCHAR(24) DEFAULT 'idle',
    last_question_id INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_practice_sessions_user_id ON practice_sessions(user_id);
CREATE INDEX idx_practice_sessions_kb_id ON practice_sessions(kb_id);

-- 题目表
CREATE TABLE questions (
    id SERIAL PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    qtype VARCHAR(16) NOT NULL,
    knowledge_point VARCHAR(128),
    stem TEXT NOT NULL,
    options JSONB,
    answer TEXT,
    analysis TEXT,
    difficulty INTEGER,
    source VARCHAR(8) DEFAULT 'ai',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_questions_session_id ON questions(session_id);
CREATE INDEX idx_questions_kb_id ON questions(kb_id);

-- 添加外键约束到 practice_sessions
ALTER TABLE practice_sessions
ADD CONSTRAINT fk_practice_sessions_last_question
FOREIGN KEY (last_question_id) REFERENCES questions(id) ON DELETE SET NULL;

-- 答题记录表
CREATE TABLE answer_records (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    question_id INTEGER NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answer TEXT NOT NULL,
    image_url TEXT,
    is_correct BOOLEAN NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_answer_records_user_id ON answer_records(user_id);
CREATE INDEX idx_answer_records_session_id ON answer_records(session_id);
CREATE INDEX idx_answer_records_question_id ON answer_records(question_id);

-- 对话轮次表
CREATE TABLE chat_turns (
    id SERIAL PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    intent VARCHAR(32),
    tool_calls JSONB,
    tool_results JSONB,
    frames JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_chat_turns_session_id ON chat_turns(session_id);

-- 会话日志表
CREATE TABLE session_logs (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    session_id INTEGER UNIQUE NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    kb_id INTEGER NOT NULL,
    started_at TIMESTAMP,
    ended_at TIMESTAMP,
    duration_sec INTEGER,
    total_questions INTEGER,
    correct_answers INTEGER,
    wrong_answers INTEGER,
    correct_rate REAL,
    knowledge_points JSONB,
    question_types JSONB,
    difficulty_stats JSONB,
    process_flow JSONB,
    summary TEXT,
    recommendation TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_session_logs_user_id ON session_logs(user_id);
CREATE INDEX idx_session_logs_session_id ON session_logs(session_id);

-- 会话过程表
CREATE TABLE session_processes (
    id SERIAL PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    step_number INTEGER,
    timestamp TIMESTAMP,
    user_message TEXT,
    agent_intent VARCHAR(32),
    tool_calls TEXT,
    result TEXT,
    question_id INTEGER,
    is_correct BOOLEAN,
    frames JSONB
);

CREATE INDEX idx_session_processes_session_id ON session_processes(session_id);

-- ======================== 转移表的所有权 ========================

ALTER TABLE users OWNER TO gexue;
ALTER TABLE grades OWNER TO gexue;
ALTER TABLE subjects OWNER TO gexue;
ALTER TABLE knowledge_bases OWNER TO gexue;
ALTER TABLE knowledge_points OWNER TO gexue;
ALTER TABLE knowledge_chunks OWNER TO gexue;
ALTER TABLE practice_sessions OWNER TO gexue;
ALTER TABLE questions OWNER TO gexue;
ALTER TABLE answer_records OWNER TO gexue;
ALTER TABLE chat_turns OWNER TO gexue;
ALTER TABLE session_logs OWNER TO gexue;
ALTER TABLE session_processes OWNER TO gexue;

-- 转移序列的所有权
ALTER SEQUENCE users_id_seq OWNER TO gexue;
ALTER SEQUENCE grades_id_seq OWNER TO gexue;
ALTER SEQUENCE subjects_id_seq OWNER TO gexue;
ALTER SEQUENCE knowledge_bases_id_seq OWNER TO gexue;
ALTER SEQUENCE knowledge_points_id_seq OWNER TO gexue;
ALTER SEQUENCE knowledge_chunks_id_seq OWNER TO gexue;
ALTER SEQUENCE practice_sessions_id_seq OWNER TO gexue;
ALTER SEQUENCE questions_id_seq OWNER TO gexue;
ALTER SEQUENCE answer_records_id_seq OWNER TO gexue;
ALTER SEQUENCE chat_turns_id_seq OWNER TO gexue;
ALTER SEQUENCE session_logs_id_seq OWNER TO gexue;
ALTER SEQUENCE session_processes_id_seq OWNER TO gexue;

-- ======================== 授予权限 ========================

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO gexue;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO gexue;

-- ======================== 插入种子数据 ========================

INSERT INTO grades (code, name) VALUES
(1, '一年级'),
(2, '二年级'),
(3, '三年级'),
(4, '四年级'),
(5, '五年级'),
(6, '六年级');

INSERT INTO subjects (code, name) VALUES
('yuwen', '语文'),
('math', '数学'),
('english', '英语');

-- ======================== 验证 ========================

SELECT '✅ Database initialization complete!' as status;

SELECT COUNT(*) as table_count FROM information_schema.tables
WHERE table_schema = 'public';

SELECT '✅ Grades: ' || COUNT(*) FROM grades as grades_count;
SELECT '✅ Subjects: ' || COUNT(*) FROM subjects as subjects_count;

SQL

echo ""
echo "✅ 数据库初始化完成！"

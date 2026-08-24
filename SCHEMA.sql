-- =========================================================================
-- GeXue 数据库架构定义
-- 包含所有表、索引和外键约束
-- ✅ 使用 pgvector 用于高效的向量存储和相似度搜索
-- =========================================================================

-- ================= 创建 pgvector 扩展 =================
CREATE EXTENSION IF NOT EXISTS vector;

-- ================= 基础表 =================

-- 用户表
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    phone VARCHAR(16) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 年级表（1-6 年级）
CREATE TABLE IF NOT EXISTS grades (
    id SERIAL PRIMARY KEY,
    code INTEGER UNIQUE NOT NULL,
    name VARCHAR(16) NOT NULL
);

-- 学科表
CREATE TABLE IF NOT EXISTS subjects (
    id SERIAL PRIMARY KEY,
    code VARCHAR(16) UNIQUE NOT NULL,
    name VARCHAR(32) NOT NULL
);

-- ================= 知识库模块 =================

-- 知识库表
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    name VARCHAR(64) NOT NULL,
    subject_id INTEGER NOT NULL,
    grade_id INTEGER NOT NULL,
    description VARCHAR(256),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_user_id ON knowledge_bases(user_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_subject_id ON knowledge_bases(subject_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_grade_id ON knowledge_bases(grade_id);

-- 知识点表（树形结构，挂在知识库下）
CREATE TABLE IF NOT EXISTS knowledge_points (
    id SERIAL PRIMARY KEY,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    parent_id INTEGER REFERENCES knowledge_points(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_knowledge_points_kb_id ON knowledge_points(kb_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_points_parent_id ON knowledge_points(parent_id);

-- 知识块表（核心表，分块文本，检索单位）
-- ✅ 使用 pgvector 的 vector(512) 类型存储嵌入向量
CREATE TABLE IF NOT EXISTS knowledge_chunks (
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

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_kb_id ON knowledge_chunks(kb_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_seq ON knowledge_chunks(seq);

-- ✅ HNSW 索引用于高效的向量相似度搜索（余弦相似度）
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_content_vec_hnsw
ON knowledge_chunks USING hnsw (content_vec vector_cosine_ops);

-- ================= 模拟测试模块 =================

-- 练习会话表（会话状态机：idle/awaiting_params/awaiting_answer/finished）
CREATE TABLE IF NOT EXISTS practice_sessions (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    kb_id INTEGER NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    state VARCHAR(24) DEFAULT 'idle',
    last_question_id INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_practice_sessions_user_id ON practice_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_practice_sessions_kb_id ON practice_sessions(kb_id);

-- 题目表（AI 出题落库）
CREATE TABLE IF NOT EXISTS questions (
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

CREATE INDEX IF NOT EXISTS idx_questions_session_id ON questions(session_id);
CREATE INDEX IF NOT EXISTS idx_questions_kb_id ON questions(kb_id);

-- 添加外键约束到 practice_sessions
ALTER TABLE practice_sessions
ADD CONSTRAINT IF NOT EXISTS fk_practice_sessions_last_question
FOREIGN KEY (last_question_id) REFERENCES questions(id) ON DELETE SET NULL;

-- 答题记录表（判分结果 → 学情统计来源）
CREATE TABLE IF NOT EXISTS answer_records (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
    question_id INTEGER NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answer TEXT NOT NULL,
    image_url TEXT,
    is_correct BOOLEAN NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_answer_records_user_id ON answer_records(user_id);
CREATE INDEX IF NOT EXISTS idx_answer_records_session_id ON answer_records(session_id);
CREATE INDEX IF NOT EXISTS idx_answer_records_question_id ON answer_records(question_id);

-- 对话轮次表（包含 Agent 工具调用日志）
CREATE TABLE IF NOT EXISTS chat_turns (
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

CREATE INDEX IF NOT EXISTS idx_chat_turns_session_id ON chat_turns(session_id);

-- 会话日志表（会话结束时生成）
CREATE TABLE IF NOT EXISTS session_logs (
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

CREATE INDEX IF NOT EXISTS idx_session_logs_user_id ON session_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_session_logs_session_id ON session_logs(session_id);

-- 会话过程表（会话过程中的一个步骤记录）
CREATE TABLE IF NOT EXISTS session_processes (
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

CREATE INDEX IF NOT EXISTS idx_session_processes_session_id ON session_processes(session_id);

-- ================= 初始数据（种子数据） =================

INSERT INTO grades (code, name) VALUES
(1, '一年级'),
(2, '二年级'),
(3, '三年级'),
(4, '四年级'),
(5, '五年级'),
(6, '六年级')
ON CONFLICT DO NOTHING;

INSERT INTO subjects (code, name) VALUES
('yuwen', '语文'),
('math', '数学'),
('english', '英语')
ON CONFLICT DO NOTHING;

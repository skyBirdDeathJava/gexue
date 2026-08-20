// Package model GORM 实体。
// 数据模型见 docs/KNOWLEDGE-BASE-SOLUTION.md 第 4 节（v2：多知识库 + 用户/年级表 + 分块）。
package model

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// EmbeddingDim 向量维度，必须与配置 embedding.dim 及迁移列 vector(N) 一致。
// 当前为 DashScope text-embedding-v3 显式指定维度 512。
const EmbeddingDim = 512

// User 家长/用户（登录主体，手机号+密码）
type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Phone        string    `gorm:"uniqueIndex;size:16" json:"phone"`
	PasswordHash string    `gorm:"size:128" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Grade 年级表（1-6）
type Grade struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Code int    `gorm:"uniqueIndex" json:"code"` // 1-6
	Name string `gorm:"size:16" json:"name"`     // 一年级...六年级
}

// Subject 学科（种子数据预置：yuwen / math / english）
type Subject struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Code string `gorm:"uniqueIndex;size:16" json:"code"`
	Name string `gorm:"size:32" json:"name"`
}

// KnowledgeBase 知识库（用户维度，按 年级+学科 组织；用户可建多个）
type KnowledgeBase struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Name      string    `gorm:"size:64" json:"name"`
	SubjectID uint      `gorm:"index" json:"subject_id"`
	GradeID   uint      `gorm:"index" json:"grade_id"`
	Desc      string    `gorm:"size:256" json:"desc"`
	CreatedAt time.Time `json:"created_at"`

	Subject *Subject `gorm:"foreignKey:SubjectID" json:"subject,omitempty"`
	Grade   *Grade   `gorm:"foreignKey:GradeID" json:"grade,omitempty"`
}

// KnowledgePoint 知识点（树，挂在知识库下）
type KnowledgePoint struct {
	ID       uint             `gorm:"primaryKey" json:"id"`
	KbID     uint             `gorm:"index" json:"kb_id"`
	Code     string           `gorm:"size:64" json:"code"`
	Name     string           `gorm:"size:128" json:"name"`
	ParentID *uint            `gorm:"index" json:"parent_id,omitempty"`
	Children []KnowledgePoint `gorm:"foreignKey:ParentID" json:"children,omitempty"`
}

// KnowledgeChunk 知识点下的分块文本（核心表，检索单位）
type KnowledgeChunk struct {
	ID          uint            `gorm:"primaryKey" json:"id"`
	KbID        uint            `gorm:"index" json:"kb_id"`
	KpID        uint            `gorm:"index" json:"kp_id"`
	Seq         int             `gorm:"index" json:"seq"` // 块内顺序
	ContentText string          `gorm:"type:text" json:"content_text"`
	ContentVec  pgvector.Vector `gorm:"type:vector(512)" json:"-"`
	Source      string          `gorm:"size:16;default:manual" json:"source"` // manual / ocr
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	KnowledgePoint *KnowledgePoint `gorm:"foreignKey:KpID" json:"knowledge_point,omitempty"`
}

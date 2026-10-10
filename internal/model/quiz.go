// 模拟测试（Agent 对话式）数据模型。
// 见 docs/AGENT-SOLUTION.md 第 6 节：会话状态机 + 题目 + 答题记录 + 对话轮次。
package model

import (
	"encoding/json"
	"time"
)

// 会话状态机（idle / awaiting_params / awaiting_answer / finished）
const (
	SessionStateIdle           = "idle"
	SessionStateAwaitingParams = "awaiting_params"
	SessionStateAwaitingAnswer = "awaiting_answer"
	SessionStateFinished       = "finished"
)

// 出题题型
const (
	QTypeSelect = "select" // 选择题
	QTypeFill   = "fill"   // 填空题
	QTypeApply  = "apply"  // 应用题
	QTypeMixed  = "mixed"  // 混合
)

// PracticeSession 模拟测试会话（聊天式，带状态机）。
// 必须绑定 kb_id：严格限定检索范围；未选知识库时 AI 主动提示。
type PracticeSession struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"index" json:"user_id"`
	KbID           uint      `gorm:"index" json:"kb_id"`
	State          string    `gorm:"size:24;default:idle" json:"state"` // idle/awaiting_params/awaiting_answer/finished
	LastQuestionID *uint     `json:"last_question_id"`                  // 当前正在做的题（nil=暂无题目）
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	LastQuestion *Question `gorm:"foreignKey:LastQuestionID" json:"last_question,omitempty"`
}

// Question 题目（AI 出题落库，供回看/错题复用）。
type Question struct {
	ID             uint            `gorm:"primaryKey" json:"id"`
	SessionID      uint            `gorm:"index" json:"session_id"`
	KbID           uint            `gorm:"index" json:"kb_id"`
	QType          string          `gorm:"column:qtype;size:16" json:"qtype"`                                // select/fill/apply
	KnowledgePoint string          `gorm:"column:knowledge_point;size:128" json:"knowledge_point,omitempty"` // 出题知识点（推题用）
	Stem           string          `gorm:"type:text" json:"stem"`
	Options        json.RawMessage `gorm:"type:jsonb" json:"options,omitempty"`
	Answer         string          `gorm:"type:text" json:"answer,omitempty"`
	Analysis       string          `gorm:"type:text" json:"analysis,omitempty"`
	Difficulty     int             `json:"difficulty"`
	Source         string          `gorm:"size:8;default:ai" json:"source"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AnswerRecord 答题记录（判分结果 → 学情统计来源）。
type AnswerRecord struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index" json:"user_id"`
	SessionID  uint      `gorm:"index" json:"session_id"`
	QuestionID uint      `gorm:"index" json:"question_id"`
	Answer     string    `gorm:"type:text" json:"answer"`              // 文本答案
	ImageUrl   string    `gorm:"type:text" json:"image_url,omitempty"` // 图片答案 URL
	IsCorrect  bool      `json:"is_correct"`
	CreatedAt  time.Time `json:"created_at"`
}

// ChatTurn 对话轮次（包含 Agent 工具调用日志）
// 结构：用户消息 + Agent 决策（意图） + 工具调用日志 + 最终回复
type ChatTurn struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	SessionID uint   `gorm:"index" json:"session_id"`
	Role      string `gorm:"size:16" json:"role"` // user / assistant
	Content   string `gorm:"type:text" json:"content"`
	// Agent 决策字段
	Intent      string          `gorm:"size:32" json:"intent,omitempty"`          // generate / answer / followup / help / summarize / chitchat
	ToolCalls   json.RawMessage `gorm:"type:jsonb" json:"tool_calls,omitempty"`   // 调用的工具及参数
	ToolResults json.RawMessage `gorm:"type:jsonb" json:"tool_results,omitempty"` // 工具执行结果摘要
	Frames      json.RawMessage `gorm:"type:jsonb" json:"frames,omitempty"`       // SSE 帧列表（题目、判分、追问等）
	CreatedAt   time.Time       `json:"created_at"`
}

// SessionSummary 会话列表项（对话历史侧栏）。
type SessionSummary struct {
	ID           uint      `json:"id"`
	KbID         uint      `json:"kb_id"`
	State        string    `json:"state"`
	Title        string    `json:"title"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	KbName       string    `json:"kb_name"`
	GradeName    string    `json:"grade_name,omitempty"`
	SubjectName  string    `json:"subject_name,omitempty"`
}

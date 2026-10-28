package model

import "time"

// TableName GORM 表名：agent_config。
func (Agent) TableName() string { return "agent_config" }

// Agent 动态 Agent 配置（DB 驱动：学科+年级 → 1 个路由 + 5 个专家子 agent）。
// 每个 agent 行描述一个可运行的 adk.ChatModelAgent 所需字段：Name/Description/Instruction/Prompt、
// Model、ToolsConfig(bindtools)、MaxIterations。年级差异化体现在 prompt 上（题型/知识点/判分逻辑）。
// 专家行经 parent_id 挂到所属路由行（transfer 单父约束：每学科年级各建独立实例）。
type Agent struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:64" json:"name"`      // 路由=yuwen_g1_router；专家=generateAgent 等
	AgentType     string    `gorm:"size:16" json:"agent_type"` // router / expert
	Role          string    `gorm:"size:16" json:"role,omitempty"` // 专家角色 generate/answer/followup/help/summarize；路由为空
	Description   string    `gorm:"type:text" json:"description"`
	Prompt        string    `gorm:"type:text" json:"prompt"` // 系统提示词（Instruction）
	SubjectID     uint      `gorm:"index" json:"subject_id"`
	SubjectName   string    `gorm:"size:32" json:"subject_name"`
	GradeID       uint      `gorm:"index" json:"grade_id"`
	GradeName     string    `gorm:"size:16" json:"grade_name"`
	ParentID      *uint     `gorm:"index" json:"parent_id,omitempty"` // 专家→所属路由；路由为 NULL
	BindTools     string    `gorm:"column:bindtools;type:text" json:"bindtools,omitempty"` // 专家绑定的工具名逗号分隔；路由为空（列名无下划线，须显式 column）
	Model         string    `gorm:"size:64" json:"model,omitempty"`  // 预留：per-agent 模型；空=全局默认
	MaxIterations int       `gorm:"default:0" json:"max_iterations,omitempty"` // 0=全局默认 8
	Status        int       `gorm:"default:1" json:"status"`         // 1=启用 0=停用
	Version       int       `gorm:"default:1" json:"version"`         // prompt 版本（缓存失效/回滚用）
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

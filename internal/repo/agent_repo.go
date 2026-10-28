package repo

import (
	"context"

	"gorm.io/gorm"

	"gexue/internal/model"
)

// AgentRepo 动态 agent 配置查询（agent 表，DB 驱动）。
type AgentRepo struct {
	db *gorm.DB
}

func NewAgentRepo(db *gorm.DB) *AgentRepo {
	return &AgentRepo{db: db}
}

// ListByScope 返回某学科+年级下启用的 agent（路由 + 专家子 agent），按 id 升序。
// 路由行 AgentType=router（每 scope 唯一），专家行 AgentType=expert（parent_id 指向路由）。
func (r *AgentRepo) ListByScope(ctx context.Context, subjectID, gradeID uint) ([]*model.Agent, error) {
	var agents []*model.Agent
	if err := r.db.WithContext(ctx).
		Where("subject_id = ? AND grade_id = ? AND status = 1", subjectID, gradeID).
		Order("id ASC").Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gexue/internal/model"
)

// QuizRepo 模拟测试：会话 / 题目 / 答题记录 / 对话轮次。
// 所有查询严格按 user_id（会话归属）与 session_id 隔离，防越权。
type QuizRepo struct{ db *gorm.DB }

func NewQuizRepo(db *gorm.DB) *QuizRepo { return &QuizRepo{db: db} }

// ---- 会话 ----

// CreateSession 新建会话（须已校验 kb_id 归属）。
func (r *QuizRepo) CreateSession(ctx context.Context, s *model.PracticeSession) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// GetSession 校验会话归属当前用户后返回。
func (r *QuizRepo) GetSession(ctx context.Context, userID, sessionID uint) (*model.PracticeSession, error) {
	var s model.PracticeSession
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", sessionID, userID).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// ListSessions 当前用户的会话（新→旧）。
func (r *QuizRepo) ListSessions(ctx context.Context, userID uint) ([]model.PracticeSession, error) {
	var ss []model.PracticeSession
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Limit(50).
		Find(&ss).Error
	return ss, err
}

// DeleteSession 删除会话（级联题目 / 答题 / 对话轮次）。
func (r *QuizRepo) DeleteSession(ctx context.Context, userID, sessionID uint) error {
	if _, err := r.GetSession(ctx, userID, sessionID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ?", sessionID).Delete(&model.ChatTurn{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", sessionID).Delete(&model.AnswerRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", sessionID).Delete(&model.Question{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", sessionID).Delete(&model.PracticeSession{}).Error
	})
}

// ListTurnsBySessions 批量取若干会话的对话轮次（正序，供列表摘要）。
func (r *QuizRepo) ListTurnsBySessions(ctx context.Context, sessionIDs []uint) ([]model.ChatTurn, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	var ts []model.ChatTurn
	err := r.db.WithContext(ctx).
		Where("session_id IN ?", sessionIDs).
		Order("id ASC").
		Find(&ts).Error
	return ts, err
}

// UpdateSession 更新会话（状态机 / LastQuestionID）。
func (r *QuizRepo) UpdateSession(ctx context.Context, s *model.PracticeSession) error {
	return r.db.WithContext(ctx).Model(s).
		Select("state", "last_question_id", "updated_at").
		Updates(map[string]any{
			"state":            s.State,
			"last_question_id": s.LastQuestionID,
			"updated_at":       s.UpdatedAt,
		}).Error
}

// ---- 题目 ----

func (r *QuizRepo) CreateQuestion(ctx context.Context, q *model.Question) error {
	return r.db.WithContext(ctx).Create(q).Error
}

// GetQuestion 取题目（校验 session + kb 归属）。
func (r *QuizRepo) GetQuestion(ctx context.Context, sessionID, questionID uint) (*model.Question, error) {
	var q model.Question
	err := r.db.WithContext(ctx).Where("id = ? AND session_id = ?", questionID, sessionID).First(&q).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &q, nil
}

// ---- 答题记录 ----

func (r *QuizRepo) CreateAnswer(ctx context.Context, a *model.AnswerRecord) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListAnswers 某会话的答题记录（按时间正序）。
func (r *QuizRepo) ListAnswers(ctx context.Context, sessionID uint) ([]model.AnswerRecord, error) {
	var as []model.AnswerRecord
	err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at").Find(&as).Error
	return as, err
}

// ---- 对话轮次 ----

// AddTurns 批量落对话轮次。
func (r *QuizRepo) AddTurns(ctx context.Context, turns []model.ChatTurn) error {
	if len(turns) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&turns).Error
}

// ListTurns 会话最近 N 轮（新→旧，由调用方反转）。
func (r *QuizRepo) ListTurns(ctx context.Context, sessionID uint, limit int) ([]model.ChatTurn, error) {
	if limit <= 0 {
		limit = 20
	}
	var ts []model.ChatTurn
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("id DESC").
		Limit(limit).
		Find(&ts).Error
	return ts, err
}

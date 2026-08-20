package repo

import (
	"context"
	"errors"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"gexue/internal/model"
)

// KnowledgeRepo 知识库 / 知识点 / 分块。
// 所有查询严格按 user_id（通过 KnowledgeBase）与 kb_id 隔离。
type KnowledgeRepo struct{ db *gorm.DB }

func NewKnowledgeRepo(db *gorm.DB) *KnowledgeRepo { return &KnowledgeRepo{db: db} }

// ---- 知识库 ----

func (r *KnowledgeRepo) CreateBase(ctx context.Context, kb *model.KnowledgeBase) error {
	return r.db.WithContext(ctx).Create(kb).Error
}

func (r *KnowledgeRepo) ListBases(ctx context.Context, userID uint) ([]model.KnowledgeBase, error) {
	var kbs []model.KnowledgeBase
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Preload("Subject").Preload("Grade").
		Order("created_at DESC").
		Find(&kbs).Error
	return kbs, err
}

// GetBase 校验知识库归属后返回。
func (r *KnowledgeRepo) GetBase(ctx context.Context, userID, kbID uint) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", kbID, userID).First(&kb).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &kb, nil
}

// ListGrades 年级表（seed 预置 1-6）。
func (r *KnowledgeRepo) ListGrades(ctx context.Context) ([]model.Grade, error) {
	var gs []model.Grade
	err := r.db.WithContext(ctx).Order("code").Find(&gs).Error
	return gs, err
}

// ListSubjects 学科表（seed 预置 语/数/英）。
func (r *KnowledgeRepo) ListSubjects(ctx context.Context) ([]model.Subject, error) {
	var ss []model.Subject
	err := r.db.WithContext(ctx).Order("id").Find(&ss).Error
	return ss, err
}

// DeleteBase 删除知识库（级联知识点+分块）。
func (r *KnowledgeRepo) DeleteBase(ctx context.Context, userID, kbID uint) error {
	if _, err := r.GetBase(ctx, userID, kbID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("kb_id = ?", kbID).Delete(&model.KnowledgeChunk{}).Error; err != nil {
			return err
		}
		if err := tx.Where("kb_id = ?", kbID).Delete(&model.KnowledgePoint{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", kbID).Delete(&model.KnowledgeBase{}).Error
	})
}

// ---- 知识点 ----

func (r *KnowledgeRepo) CreatePoint(ctx context.Context, kp *model.KnowledgePoint) error {
	return r.db.WithContext(ctx).Create(kp).Error
}

// ListPoints 返回知识库下知识点（扁平），由 service 组树。
func (r *KnowledgeRepo) ListPoints(ctx context.Context, kbID uint) ([]model.KnowledgePoint, error) {
	var kps []model.KnowledgePoint
	err := r.db.WithContext(ctx).Where("kb_id = ?", kbID).Order("id").Find(&kps).Error
	return kps, err
}

// ---- 分块 ----

// SaveChunks 批量写分块（事务）。
func (r *KnowledgeRepo) SaveChunks(ctx context.Context, chunks []model.KnowledgeChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&chunks).Error
}

// ListChunksByPoint 某知识点下的全部分块（按 seq 排序）。
func (r *KnowledgeRepo) ListChunksByPoint(ctx context.Context, kpID uint) ([]model.KnowledgeChunk, error) {
	var chunks []model.KnowledgeChunk
	err := r.db.WithContext(ctx).Where("kp_id = ?", kpID).Order("seq").Find(&chunks).Error
	return chunks, err
}

// DeleteChunk 删除某分块（校验 kb + 知识点归属）。
func (r *KnowledgeRepo) DeleteChunk(ctx context.Context, kbID, kpID, chunkID uint) error {
	res := r.db.WithContext(ctx).Where("id = ? AND kb_id = ? AND kp_id = ?", chunkID, kbID, kpID).Delete(&model.KnowledgeChunk{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePoint 删除知识点（级联其全部分块）。
func (r *KnowledgeRepo) DeletePoint(ctx context.Context, kbID, kpID uint) error {
	res := r.db.WithContext(ctx).Where("id = ? AND kb_id = ?", kpID, kbID).Delete(&model.KnowledgePoint{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return r.db.WithContext(ctx).Where("kp_id = ?", kpID).Delete(&model.KnowledgeChunk{}).Error
}

// SearchInKb 库内语义检索（HNSW + 余弦），按 chunk 召回。
func (r *KnowledgeRepo) SearchInKb(ctx context.Context, kbID uint, vec pgvector.Vector, topK int) ([]model.KnowledgeChunk, error) {
	if topK <= 0 {
		topK = 5
	}
	var chunks []model.KnowledgeChunk
	err := r.db.WithContext(ctx).
		Where("kb_id = ?", kbID).
		Where("content_vec IS NOT NULL").
		Order(gorm.Expr("content_vec <=> ?::vector", vec)).
		Limit(topK).
		Find(&chunks).Error
	return chunks, err
}

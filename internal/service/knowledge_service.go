package service

import (
	"context"

	"github.com/pgvector/pgvector-go"

	"gexue/internal/embedding"
	"gexue/internal/model"
	"gexue/internal/pkg/chunk"
	"gexue/internal/repo"
)

// KnowledgeService 知识库编排。
type KnowledgeService struct {
	repo *repo.KnowledgeRepo
	emb  embedding.Embedder
	// 分块参数（config chunk 段）
	maxChunk int
	overlap  int
}

func NewKnowledgeService(r *repo.KnowledgeRepo, e embedding.Embedder, maxChunk, overlap int) *KnowledgeService {
	if maxChunk <= 0 {
		maxChunk = 500
	}
	if overlap <= 0 {
		overlap = 60
	}
	return &KnowledgeService{repo: r, emb: e, maxChunk: maxChunk, overlap: overlap}
}

// CreateBase 创建知识库（年级+学科维度）。
func (s *KnowledgeService) CreateBase(ctx context.Context, userID uint, name string, subjectID, gradeID uint, desc string) (*model.KnowledgeBase, error) {
	kb := &model.KnowledgeBase{
		UserID:    userID,
		Name:      name,
		SubjectID: subjectID,
		GradeID:   gradeID,
		Desc:      desc,
	}
	if err := s.repo.CreateBase(ctx, kb); err != nil {
		return nil, err
	}
	return kb, nil
}

// CreatePoint 在知识库下建知识点（parent_id<=0 视为根节点，存 NULL）。
func (s *KnowledgeService) CreatePoint(ctx context.Context, kbID uint, code, name string, parentID *uint) (*model.KnowledgePoint, error) {
	if parentID != nil && *parentID == 0 {
		parentID = nil // 根节点：无父
	}
	kp := &model.KnowledgePoint{KbID: kbID, Code: code, Name: name, ParentID: parentID}
	if err := s.repo.CreatePoint(ctx, kp); err != nil {
		return nil, err
	}
	return kp, nil
}

// CreateChunks 录入：校验 KB 归属 → 分块 → 向量化 → 入库。
func (s *KnowledgeService) CreateChunks(ctx context.Context, userID, kbID, kpID uint, content, source string) ([]model.KnowledgeChunk, error) {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	if source == "" {
		source = "manual"
	}
	parts := chunk.Split(content, s.maxChunk, s.overlap)
	if len(parts) == 0 {
		return nil, nil
	}
	vecs, err := s.emb.Embed(ctx, parts)
	if err != nil {
		return nil, err
	}
	chunks := make([]model.KnowledgeChunk, 0, len(parts))
	for i, p := range parts {
		chunks = append(chunks, model.KnowledgeChunk{
			KbID:        kbID,
			KpID:        kpID,
			Seq:         i,
			ContentText: p,
			ContentVec:  pgvector.NewVector(vecs[i]),
			Source:      source,
		})
	}
	if err := s.repo.SaveChunks(ctx, chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListBases 我的知识库列表。
func (s *KnowledgeService) ListBases(ctx context.Context, userID uint) ([]model.KnowledgeBase, error) {
	return s.repo.ListBases(ctx, userID)
}

// ListMeta 年级+学科元数据（建库下拉）。
func (s *KnowledgeService) ListMeta(ctx context.Context) ([]model.Grade, []model.Subject, error) {
	grades, err := s.repo.ListGrades(ctx)
	if err != nil {
		return nil, nil, err
	}
	subjects, err := s.repo.ListSubjects(ctx)
	if err != nil {
		return nil, nil, err
	}
	return grades, subjects, nil
}

// DeleteBase 删除知识库（级联）。
func (s *KnowledgeService) DeleteBase(ctx context.Context, userID, kbID uint) error {
	return s.repo.DeleteBase(ctx, userID, kbID)
}

// ListPointTree 知识库下知识点树（扁平 → 树）。
func (s *KnowledgeService) ListPointTree(ctx context.Context, userID, kbID uint) ([]model.KnowledgePoint, error) {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	points, err := s.repo.ListPoints(ctx, kbID)
	if err != nil {
		return nil, err
	}
	return buildTree(points), nil
}

// buildTree 依据 ParentID 组装树（parent_id 为空为根）。
// 自顶向下递归挂载：从根开始逐层 attach，避免"先拷贝子节点、后补孙节点"的时序丢失。
func buildTree(points []model.KnowledgePoint) []model.KnowledgePoint {
	byID := make(map[uint]model.KnowledgePoint, len(points))
	children := make(map[uint][]uint) // parentID -> 子ID
	for _, p := range points {
		p.Children = nil
		byID[p.ID] = p
		if p.ParentID != nil {
			children[*p.ParentID] = append(children[*p.ParentID], p.ID)
		}
	}
	var attach func(id uint) model.KnowledgePoint
	attach = func(id uint) model.KnowledgePoint {
		p := byID[id]
		for _, cid := range children[id] {
			p.Children = append(p.Children, attach(cid))
		}
		return p
	}
	var roots []model.KnowledgePoint
	for _, p := range byID {
		// 根：无父，或父不在本知识库（孤儿兜底为根）
		if p.ParentID == nil {
			roots = append(roots, attach(p.ID))
		} else if _, ok := byID[*p.ParentID]; !ok {
			roots = append(roots, attach(p.ID))
		}
	}
	return roots
}

// ListPointChunks 某知识点下的分块列表。
func (s *KnowledgeService) ListPointChunks(ctx context.Context, userID, kbID, kpID uint) ([]model.KnowledgeChunk, error) {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	return s.repo.ListChunksByPoint(ctx, kpID)
}

// DeleteChunk 删除分块（分块须属于该知识点）。
func (s *KnowledgeService) DeleteChunk(ctx context.Context, userID, kbID, kpID, chunkID uint) error {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return err
	}
	return s.repo.DeleteChunk(ctx, kbID, kpID, chunkID)
}

// DeletePoint 删除知识点（级联分块）。
func (s *KnowledgeService) DeletePoint(ctx context.Context, userID, kbID, kpID uint) error {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return err
	}
	return s.repo.DeletePoint(ctx, kbID, kpID)
}

// SearchInKb 库内语义检索（出题/回忆用）。
func (s *KnowledgeService) SearchInKb(ctx context.Context, userID, kbID uint, query string, topK int) ([]model.KnowledgeChunk, error) {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	vecs, err := s.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return s.repo.SearchInKb(ctx, kbID, pgvector.NewVector(vecs[0]), topK)
}

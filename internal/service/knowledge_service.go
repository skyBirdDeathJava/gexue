package service

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/pgvector/pgvector-go"
	"go.uber.org/zap"

	"gexue/internal/embedding"
	"gexue/internal/model"
	"gexue/internal/pkg/chunk"
	"gexue/internal/pkg/document"
	"gexue/internal/pkg/volcengine"
	"gexue/internal/repo"
	"gexue/internal/retrieval"
)

// KnowledgeService 知识库编排。
type KnowledgeService struct {
	repo      *repo.KnowledgeRepo
	emb       embedding.Embedder
	retriever *retrieval.Retriever                             // 双路召回检索编排（nil 时回退旧向量检索）
	ossUpload func(objectKey, filePath string) (string, error) // OSS 上传函数
	ocrClient *volcengine.OCRClient                            // 火山引擎 OCR 客户端
	logger    *zap.Logger                                      // 日志记录器
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
	logger, _ := zap.NewProduction()
	return &KnowledgeService{repo: r, emb: e, maxChunk: maxChunk, overlap: overlap, logger: logger}
}

// SetRetriever 注入双路召回检索编排器（RRF + rerank）。
func (s *KnowledgeService) SetRetriever(r *retrieval.Retriever) {
	s.retriever = r
}

// SetOSSUpload 注入 OSS 上传函数
func (s *KnowledgeService) SetOSSUpload(fn func(objectKey, filePath string) (string, error)) {
	s.ossUpload = fn
}

// SetOCRClient 注入火山引擎 OCR 客户端
func (s *KnowledgeService) SetOCRClient(client *volcengine.OCRClient) {
	s.ocrClient = client
}

// CreateBase 创建知识库（年级+学科维度）
func (s *KnowledgeService) CreateBase(ctx context.Context, userID uint, name string, subjectID, gradeID uint, desc string) (*model.KnowledgeBase, error) {
	kb := &model.KnowledgeBase{
		UserID:      userID,
		Name:        name,
		SubjectID:   subjectID,
		GradeID:     gradeID,
		Description: desc,
	}
	if err := s.repo.CreateBase(ctx, kb); err != nil {
		return nil, err
	}
	return kb, nil
}

// CreateChunks 录入：校验 KB 归属 → 分块 → 向量化 → 入库（旧 API，保留兼容）。
// Deprecated: 使用 CreateChunksFromFile 替代，用户无需手动指定知识点。
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

// CreateChunksFromFile 从文件上传：
// 1. 原始文件上传到 OSS
// 2. 提取文本 → 分块 → 向量化
// 3. 所有分块关联原文件的 OSS URL 入库
// filePath: 临时文件路径，fileName: 原始文件名
func (s *KnowledgeService) CreateChunksFromFile(ctx context.Context, userID, kbID uint, filePath, fileName string) ([]model.KnowledgeChunk, error) {
	// 1. 验证知识库权限
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}

	// 2. 上传原始文件到 OSS（如果配置了 OSS）
	var ossURL string
	if s.ossUpload != nil {
		var err error
		objectKey := fileName // 使用原始文件名作为对象键前缀
		ossURL, err = s.ossUpload(objectKey, filePath)
		if err != nil {
			return nil, err
		}
	}

	// 3. 提取文本
	text, err := document.ExtractTextWithOCRClient(ctx, filePath, s.ocrClient)
	if err != nil {
		return nil, err
	}

	textRunes := []rune(text)
	s.logger.Info("OCR 文本提取完成",
		zap.String("file", fileName),
		zap.Int("char_count", len(textRunes)),
		zap.Int("byte_length", len(text)),
		zap.String("first_500_chars", truncate(text, 500)))

	// 标准化文本：移除多余换行，节省 token
	originalLen := len(textRunes)
	text = normalizeText(text)
	normalizedLen := len([]rune(text))

	s.logger.Info("文本标准化完成",
		zap.String("file", fileName),
		zap.Int("original_chars", originalLen),
		zap.Int("normalized_chars", normalizedLen),
		zap.Int("chars_saved", originalLen-normalizedLen))

	// 4. 分块
	parts := chunk.Split(text, s.maxChunk, s.overlap)
	if len(parts) == 0 {
		return nil, nil
	}

	s.logger.Info("文本分块完成",
		zap.String("file", fileName),
		zap.Int("chunk_count", len(parts)),
		zap.Int("max_chunk", s.maxChunk),
		zap.Int("overlap", s.overlap))

	// 打印前几个分块用于调试
	for i := 0; i < len(parts) && i < 3; i++ {
		s.logger.Debug("分块详情",
			zap.Int("chunk_index", i),
			zap.Int("chunk_length", len(parts[i])),
			zap.String("content_preview", truncate(parts[i], 200)))
	}

	// 5. 向量化
	vecs, err := s.emb.Embed(ctx, parts)
	if err != nil {
		return nil, err
	}

	var fileSize int64
	if info, statErr := os.Stat(filePath); statErr == nil {
		fileSize = info.Size()
	}

	// 6. 创建分块对象（所有分块关联同一个原文件的 OSS URL）
	chunks := make([]model.KnowledgeChunk, 0, len(parts))
	for i, p := range parts {
		chunks = append(chunks, model.KnowledgeChunk{
			KbID:        kbID,
			Seq:         i,
			ContentText: p,                           // 分块后的文本内容
			ContentVec:  pgvector.NewVector(vecs[i]), // 向量化的分块
			Source:      "upload",
			FileName:    fileName, // 原始文件名
			FileSize:    fileSize,
			OSSUrl:      ossURL, // 原始文件的 OSS URL（所有分块相同）
		})
	}

	// 7. 入库
	if err := s.repo.SaveChunks(ctx, chunks); err != nil {
		return nil, err
	}

	return chunks, nil
}

// GetBase 知识库详情（含年级/学科）。
func (s *KnowledgeService) GetBase(ctx context.Context, userID, kbID uint) (*model.KnowledgeBase, error) {
	return s.repo.GetBaseWithMeta(ctx, userID, kbID)
}

// ListFiles 知识库下已入库文档。
func (s *KnowledgeService) ListFiles(ctx context.Context, userID, kbID uint) ([]model.KnowledgeFile, error) {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	return s.repo.ListFiles(ctx, kbID)
}

// DeleteFile 删除某文档的全部分块。
func (s *KnowledgeService) DeleteFile(ctx context.Context, userID, kbID uint, fileName string) error {
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return err
	}
	return s.repo.DeleteChunksByFile(ctx, kbID, fileName)
}

// ListBases 我的知识库列表
func (s *KnowledgeService) ListBases(ctx context.Context, userID uint) ([]model.KnowledgeBase, error) {
	return s.repo.ListBases(ctx, userID)
}

// ListMeta 年级+学科元数据（建库下拉）
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

// DeleteBase 删除知识库（级联）
func (s *KnowledgeService) DeleteBase(ctx context.Context, userID, kbID uint) error {
	return s.repo.DeleteBase(ctx, userID, kbID)
}

// SearchInKb 库内语义检索（出题/回忆用）。
// 已注入 retriever 时走双路召回（向量 + BM25）→ RRF 融合 → rerank 精排；
// 未注入（兼容旧调用）则回退为单路向量检索。
func (s *KnowledgeService) SearchInKb(ctx context.Context, userID, kbID uint, query string, topK int) ([]model.KnowledgeChunk, error) {
	if s.retriever != nil {
		return s.retriever.Search(ctx, userID, kbID, query, topK)
	}
	if _, err := s.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	vecs, err := s.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return s.repo.SearchInKb(ctx, kbID, pgvector.NewVector(vecs[0]), topK)
}

// truncate 截断字符串用于日志打印
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// normalizeText 标准化文本：移除多余换行，节省存储和 token
// 处理规则：
// 1. 多个连续换行替换为单个换行
// 2. 移除行尾空白
// 3. 移除纯空白行
func normalizeText(text string) string {
	// 1. 将多个连续换行（包括有空格的）替换为单个换行
	re := regexp.MustCompile(`\n[\s\n]*`)
	text = re.ReplaceAllString(text, "\n")

	// 2. 按行处理，移除行尾空白
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 3. 移除纯空白行（保留内容行）
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	// 4. 用单个换行重新连接
	result := strings.Join(cleanLines, "\n")
	return strings.TrimSpace(result)
}

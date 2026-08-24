package api

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gexue/internal/api/resp"
	"gexue/internal/model"
	"gexue/internal/repo"
	"gexue/internal/service"
)

// handleErr 统一错误映射：not found → 404，其余 → 500
func handleErr(c *gin.Context, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		resp.Fail(c, resp.CodeNotFound, "not found")
		return
	}
	resp.Fail(c, resp.CodeInternal, err.Error())
}

// validateDocxFile 验证DOCX文件的有效性
func validateDocxFile(filePath string) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if fileInfo.Size() == 0 {
		return errors.New("file is empty")
	}

	// 检查ZIP签名
	data := make([]byte, 4)
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Read(data); err != nil {
		return err
	}

	if data[0] != 0x50 || data[1] != 0x4B {
		return errors.New("not a valid ZIP file (invalid DOCX format)")
	}

	// 尝试打开ZIP验证结构完整
	_, err = zip.OpenReader(filePath)
	return err
}

// validatePdfFile 验证PDF文件的有效性
func validatePdfFile(filePath string) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if fileInfo.Size() == 0 {
		return errors.New("file is empty")
	}

	// 检查PDF签名
	data := make([]byte, 4)
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Read(data); err != nil {
		return err
	}

	// PDF 文件应该以 %PDF 开头
	if string(data[:4]) != "%PDF" {
		return errors.New("not a valid PDF file (invalid PDF header)")
	}

	return nil
}

// checkPdfToolsAvailability 检查PDF处理工具是否可用（仅用于诊断）
func checkPdfToolsAvailability() {
	// 这是一个可选的诊断检查，不影响上传过程
	// 在生产环境中，如果需要OCR，相关错误会在处理时返回
	// 这里主要用于开发环境调试
}

// KnowledgeHandler 知识库：创建 / 列表 / 删除 / 文件上传 / 检索
type KnowledgeHandler struct {
	svc *service.KnowledgeService
}

func NewKnowledgeHandler(svc *service.KnowledgeService) *KnowledgeHandler {
	return &KnowledgeHandler{svc: svc}
}

// Meta 年级+学科元数据（建库下拉用）
func (h *KnowledgeHandler) Meta(c *gin.Context) {
	grades, subjects, err := h.svc.ListMeta(c.Request.Context())
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, gin.H{"grades": grades, "subjects": subjects})
}

func (h *KnowledgeHandler) CreateBase(c *gin.Context) {
	var in struct {
		Name        string `json:"name" binding:"required"`
		SubjectID   uint   `json:"subject_id" binding:"required"`
		GradeID     uint   `json:"grade_id" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	kb, err := h.svc.CreateBase(c.Request.Context(), c.GetUint("user_id"), in.Name, in.SubjectID, in.GradeID, in.Description)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kb)
}

func (h *KnowledgeHandler) ListBases(c *gin.Context) {
	kbs, err := h.svc.ListBases(c.Request.Context(), c.GetUint("user_id"))
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kbs)
}

func (h *KnowledgeHandler) GetBase(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	kb, err := h.svc.GetBase(c.Request.Context(), c.GetUint("user_id"), uint(id))
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kb)
}

func (h *KnowledgeHandler) ListFiles(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	files, err := h.svc.ListFiles(c.Request.Context(), c.GetUint("user_id"), uint(id))
	if err != nil {
		handleErr(c, err)
		return
	}
	if files == nil {
		files = []model.KnowledgeFile{}
	}
	resp.OK(c, files)
}

func (h *KnowledgeHandler) DeleteFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	fileName, ok := c.GetQuery("file_name")
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "file_name 必填")
		return
	}
	if err := h.svc.DeleteFile(c.Request.Context(), c.GetUint("user_id"), uint(id), fileName); err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, nil)
}

func (h *KnowledgeHandler) DeleteBase(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	if err := h.svc.DeleteBase(c.Request.Context(), c.GetUint("user_id"), uint(id)); err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, nil)
}

func (h *KnowledgeHandler) SearchInKb(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	var in struct {
		Query string `json:"query" binding:"required"`
		TopK  int    `json:"top_k"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	chunks, err := h.svc.SearchInKb(c.Request.Context(), c.GetUint("user_id"), uint(id), in.Query, in.TopK)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, chunks)
}

// UploadFile 上传文件到知识库（自动提取+分块+向量化）
// POST /api/knowledge-bases/:id/upload-file
// 支持格式：txt, md, docx, pdf, jpg, jpeg, png
func (h *KnowledgeHandler) UploadFile(c *gin.Context) {
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid kb id")
		return
	}

	// 接收文件
	file, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "file upload failed: "+err.Error())
		return
	}

	// 限制文件大小（50MB）
	const maxFileSize = 50 * 1024 * 1024
	if file.Size > maxFileSize {
		resp.Fail(c, resp.CodeBadRequest, "file size exceeds 50MB limit")
		return
	}

	// 验证文件类型
	ext := strings.ToLower(filepath.Ext(file.Filename))
	validExts := map[string]bool{
		".txt":      true,
		".md":       true,
		".markdown": true,
		".docx":     true,
		".pdf":      true,
		".jpg":      true,
		".jpeg":     true,
		".png":      true,
	}
	if !validExts[ext] {
		resp.Fail(c, resp.CodeBadRequest, "unsupported file type: "+ext)
		return
	}

	// 保存到临时目录（使用当前目录下的 tmp 文件夹避免权限问题）
	tmpDir := "./tmp"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		resp.Fail(c, resp.CodeInternal, "failed to create temp dir: "+err.Error())
		return
	}
	tmpPath := filepath.Join(tmpDir, file.Filename)
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		resp.Fail(c, resp.CodeInternal, "failed to save file: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)

	// 验证文件是否完整（对DOCX/PDF等特定格式的基本检查）
	if ext == ".docx" {
		if err := validateDocxFile(tmpPath); err != nil {
			resp.Fail(c, resp.CodeBadRequest, "invalid docx file: "+err.Error())
			return
		}
	} else if ext == ".pdf" {
		if err := validatePdfFile(tmpPath); err != nil {
			resp.Fail(c, resp.CodeBadRequest, "invalid pdf file: "+err.Error())
			return
		}
		// 检查OCR工具依赖（可选警告）
		checkPdfToolsAvailability()
	}

	// 提取文本、分块、向量化入库
	chunks, err := h.svc.CreateChunksFromFile(
		c.Request.Context(),
		c.GetUint("user_id"),
		uint(kbID),
		tmpPath,
		file.Filename,
	)
	if err != nil {
		handleErr(c, err)
		return
	}

	// 返回成功
	resp.OK(c, gin.H{
		"file_name":    file.Filename,
		"chunks_count": len(chunks),
		"chunks":       chunks,
	})
}

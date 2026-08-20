package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"gexue/internal/api/resp"
	"gexue/internal/repo"
	"gexue/internal/service"
)

// handleErr 统一错误映射：not found → 404，其余 → 500。
func handleErr(c *gin.Context, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		resp.Fail(c, resp.CodeNotFound, "not found")
		return
	}
	resp.Fail(c, resp.CodeInternal, err.Error())
}

// KnowledgeHandler 知识库：创建 / 列表 / 删除 / 知识点 / 录入 / 检索。
type KnowledgeHandler struct {
	svc *service.KnowledgeService
}

func NewKnowledgeHandler(svc *service.KnowledgeService) *KnowledgeHandler {
	return &KnowledgeHandler{svc: svc}
}

// Register 路由（均需 AuthRequired）
func (h *KnowledgeHandler) Register(g *gin.RouterGroup) {
	g.GET("/meta", h.meta)
	g.POST("/knowledge-bases", h.createBase)
	g.GET("/knowledge-bases", h.listBases)
	g.DELETE("/knowledge-bases/:id", h.deleteBase)
	g.POST("/knowledge-bases/:id/knowledge-points", h.createPoint)
	g.GET("/knowledge-bases/:id/knowledge-points", h.listPoints)
	g.POST("/knowledge-bases/:id/knowledge-points/:kpid/chunks", h.createChunks)
	g.GET("/knowledge-bases/:id/knowledge-points/:kpid/chunks", h.listChunks)
	g.DELETE("/knowledge-bases/:id/knowledge-points/:kpid/chunks/:chunkId", h.deleteChunk)
	g.DELETE("/knowledge-bases/:id/knowledge-points/:kpid", h.deletePoint)
	g.POST("/knowledge-bases/:id/search", h.searchInKb)
}

// meta 年级+学科元数据（建库下拉用）。
func (h *KnowledgeHandler) meta(c *gin.Context) {
	grades, subjects, err := h.svc.ListMeta(c.Request.Context())
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, gin.H{"grades": grades, "subjects": subjects})
}

func (h *KnowledgeHandler) createBase(c *gin.Context) {
	var in struct {
		Name      string `json:"name" binding:"required"`
		SubjectID uint   `json:"subject_id" binding:"required"`
		GradeID   uint   `json:"grade_id" binding:"required"`
		Desc      string `json:"desc"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	kb, err := h.svc.CreateBase(c.Request.Context(), c.GetUint("user_id"), in.Name, in.SubjectID, in.GradeID, in.Desc)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kb)
}

func (h *KnowledgeHandler) listBases(c *gin.Context) {
	kbs, err := h.svc.ListBases(c.Request.Context(), c.GetUint("user_id"))
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kbs)
}

func (h *KnowledgeHandler) deleteBase(c *gin.Context) {
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

func (h *KnowledgeHandler) createPoint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	var in struct {
		Code     string `json:"code"`
		Name     string `json:"name" binding:"required"`
		ParentID *uint  `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	kp, err := h.svc.CreatePoint(c.Request.Context(), uint(id), in.Code, in.Name, in.ParentID)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, kp)
}

func (h *KnowledgeHandler) createChunks(c *gin.Context) {
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	kpID, err := strconv.ParseUint(c.Param("kpid"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid kpid")
		return
	}
	var in struct {
		Content string `json:"content_text" binding:"required"`
		Source  string `json:"source"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	chunks, err := h.svc.CreateChunks(c.Request.Context(), c.GetUint("user_id"), uint(kbID), uint(kpID), in.Content, in.Source)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, chunks)
}

// listChunks 某知识点下已录入的分块列表。
func (h *KnowledgeHandler) listChunks(c *gin.Context) {
	kbID, kpID, err := parseKbKpParams(c)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	chunks, err := h.svc.ListPointChunks(c.Request.Context(), c.GetUint("user_id"), kbID, kpID)
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, chunks)
}

func (h *KnowledgeHandler) deleteChunk(c *gin.Context) {
	kbID, kpID, err := parseKbKpParams(c)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	chunkID, err := strconv.ParseUint(c.Param("chunkId"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid chunk id")
		return
	}
	// kpID 参与校验：分块须属于该知识点
	if err := h.svc.DeleteChunk(c.Request.Context(), c.GetUint("user_id"), kbID, kpID, uint(chunkID)); err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, nil)
}

func (h *KnowledgeHandler) deletePoint(c *gin.Context) {
	kbID, kpID, err := parseKbKpParams(c)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if err := h.svc.DeletePoint(c.Request.Context(), c.GetUint("user_id"), kbID, kpID); err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, nil)
}

// parseKbKpParams 解析 :id 与 :kpid 路径参数。
func parseKbKpParams(c *gin.Context) (uint, uint, error) {
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	kpID, err := strconv.ParseUint(c.Param("kpid"), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return uint(kbID), uint(kpID), nil
}

func (h *KnowledgeHandler) listPoints(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid id")
		return
	}
	tree, err := h.svc.ListPointTree(c.Request.Context(), c.GetUint("user_id"), uint(id))
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, tree)
}

func (h *KnowledgeHandler) searchInKb(c *gin.Context) {
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

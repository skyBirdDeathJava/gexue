package api

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gexue/internal/api/resp"
	"gexue/internal/model"
	"gexue/internal/repo"
	"gexue/internal/service"
)

// QuizHandler 模拟测试（Agent 对话式）：会话 / 聊天（SSE）。
// 见 docs/AGENT-SOLUTION.md §9。
type QuizHandler struct {
	svc *service.QuizService
}

func NewQuizHandler(svc *service.QuizService) *QuizHandler {
	return &QuizHandler{svc: svc}
}

// CreateSession 新建模拟测试会话：kb_id 必填且须属于当前用户。
// 无效知识库 → 返回引导提示"请先选择知识库"（§1 关键交互规则 1）。
func (h *QuizHandler) CreateSession(c *gin.Context) {
	var in struct {
		KbID uint `json:"kb_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "kb_id 必填，请先选择一个知识库")
		return
	}
	sess, err := h.svc.CreateSession(c.Request.Context(), c.GetUint("user_id"), in.KbID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			resp.Fail(c, resp.CodeNotFound, "请先选择知识库：该知识库不存在或不属于你")
			return
		}
		handleErr(c, err)
		return
	}
	resp.OK(c, gin.H{"session_id": sess.ID, "state": sess.State, "kb_id": sess.KbID})
}

func (h *QuizHandler) ListSessions(c *gin.Context) {
	list, err := h.svc.ListSessions(c.Request.Context(), c.GetUint("user_id"))
	if err != nil {
		handleErr(c, err)
		return
	}
	if list == nil {
		list = []model.SessionSummary{}
	}
	resp.OK(c, list)
}

func (h *QuizHandler) DeleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid session id")
		return
	}
	if err := h.svc.DeleteSession(c.Request.Context(), c.GetUint("user_id"), uint(id)); err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, nil)
}

// getSession 会话上下文（状态/最近题/历史，恢复用）。
func (h *QuizHandler) GetSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "invalid session id")
		return
	}
	sess, turns, err := h.svc.GetSession(c.Request.Context(), c.GetUint("user_id"), uint(id))
	if err != nil {
		handleErr(c, err)
		return
	}
	resp.OK(c, gin.H{"session": sess, "history": turns})
}

// chat 聊天接口：支持文本和图片答案（应用题）。
// 请求格式：
// 1. 纯文本：{"session_id": 1, "message": "答案内容"}
// 2. 图片答案：form-data with "session_id", "message" (optional), "image" file
// SSE 流式返回帧。
func (h *QuizHandler) Chat(c *gin.Context) {
	var in struct {
		SessionID uint   `form:"session_id" json:"session_id"`
		Message   string `form:"message" json:"message"`
		Intent    string `form:"intent" json:"intent"` // answer（默认）/ question：仅图片上传时有意义
	}

	// 优先尝试解析 JSON（文本答案）
	if c.ContentType() == "application/json" {
		if err := c.ShouldBindJSON(&in); err != nil {
			resp.Fail(c, resp.CodeBadRequest, "session_id 与 message 必填")
			return
		}
	} else if c.ContentType() == "multipart/form-data" {
		// 多部分表单：文本字段 + 文件
		if err := c.ShouldBind(&in); err != nil {
			resp.Fail(c, resp.CodeBadRequest, "session_id 必填，message 和 image 可选")
			return
		}

		// 处理图片上传：先 OCR 成文字，再交给 Agent（LLM 为纯文本模型，无法直接看图）
		header, err := c.FormFile("image")
		if err == nil && header != nil {
			file, err := header.Open()
			if err != nil {
				resp.Fail(c, resp.CodeBadRequest, "图片读取失败")
				return
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				resp.Fail(c, resp.CodeBadRequest, "图片读取失败")
				return
			}

			// 1. 上传 OSS 留档（失败不阻断 OCR）
			if _, err := h.svc.UploadAnswerImageBytes(c.Request.Context(), c.GetUint("user_id"), data, header.Filename); err != nil {
				resp.Fail(c, resp.CodeInternal, "图片上传失败："+err.Error())
				return
			}

			// 2. OCR 识别为文字
			ocrText, err := h.svc.OCRImageBytes(c.Request.Context(), data)
			if err != nil || strings.TrimSpace(ocrText) == "" {
				resp.Fail(c, resp.CodeInternal, "图片识别失败，请上传清晰图片或直接输入文字")
				return
			}

			// 3. 按意图拼成文本消息交给 Agent
			if strings.ToLower(in.Intent) == "question" {
				in.Message = "【学生上传题目图片，OCR 识别如下】\n" + ocrText
			} else {
				in.Message = "【学生上传答案图片，OCR 识别如下】\n" + ocrText
			}
		} else if in.Message == "" {
			resp.Fail(c, resp.CodeBadRequest, "必须提供 message 或 image 中的至少一个")
			return
		}
	} else {
		resp.Fail(c, resp.CodeBadRequest, "Content-Type 必须是 application/json 或 multipart/form-data")
		return
	}

	if in.SessionID == 0 {
		resp.Fail(c, resp.CodeBadRequest, "session_id 必填")
		return
	}
	if in.Message == "" {
		resp.Fail(c, resp.CodeBadRequest, "message 或 image 必须提供其中一个")
		return
	}

	frames, err := h.svc.Chat(c.Request.Context(), c.GetUint("user_id"), in.SessionID, in.Message)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			resp.Fail(c, resp.CodeNotFound, "会话不存在，请先创建模拟测试会话")
			return
		}
		handleErr(c, err)
		return
	}

	// SSE 逐帧推送
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Stream(func(w io.Writer) bool {
		for _, f := range frames {
			if f == nil {
				continue
			}
			c.SSEvent("message", f)
		}
		// 结束帧
		c.SSEvent("message", gin.H{"kind": "done"})
		return false
	})
}

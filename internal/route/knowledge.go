package route

import (
	"github.com/gin-gonic/gin"

	"gexue/internal/api/middleware"
)

// registerKnowledge 知识库模块路由（均需登录）
func registerKnowledge(r *gin.Engine, deps Deps) {
	if deps.Knowledge == nil {
		return
	}
	g := r.Group("/api")
	g.Use(middleware.AuthRequired(deps.JWTSecret))

	h := deps.Knowledge
	g.GET("/meta", h.Meta)
	g.POST("/knowledge-bases", h.CreateBase)
	g.GET("/knowledge-bases", h.ListBases)
	g.GET("/knowledge-bases/:id", h.GetBase)
	g.GET("/knowledge-bases/:id/files", h.ListFiles)
	g.DELETE("/knowledge-bases/:id/files", h.DeleteFile)
	g.DELETE("/knowledge-bases/:id", h.DeleteBase)
	g.POST("/knowledge-bases/:id/search", h.SearchInKb)
	// 文件上传（自动提取+分块+向量化）
	g.POST("/knowledge-bases/:id/upload-file", h.UploadFile)
}

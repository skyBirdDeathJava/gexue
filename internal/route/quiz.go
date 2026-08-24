package route

import (
	"github.com/gin-gonic/gin"

	"gexue/internal/api/middleware"
)

// registerQuiz 模拟测试 Agent 模块路由（均需登录）。
func registerQuiz(r *gin.Engine, deps Deps) {
	if deps.Quiz == nil {
		return
	}
	g := r.Group("/api")
	g.Use(middleware.AuthRequired(deps.JWTSecret))

	h := deps.Quiz
	g.POST("/mock-test/sessions", h.CreateSession)
	g.GET("/mock-test/sessions", h.ListSessions)
	g.GET("/mock-test/sessions/:id", h.GetSession)
	g.DELETE("/mock-test/sessions/:id", h.DeleteSession)
	g.POST("/mock-test/chat", h.Chat)
}

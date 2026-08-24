// Package route 各业务模块路由，按模块拆分（auth / knowledge / quiz）。
// 容器入口：NewRouter 组装 Gin Engine 并聚合各模块注册。
package route

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gexue/internal/api"
	"gexue/internal/api/middleware"
	"gexue/internal/api/resp"
)

// Router 路由容器。
type Router struct {
	engine *gin.Engine
	log    *zap.Logger
}

// Deps 路由依赖（service 由 main 装配后注入）。
type Deps struct {
	Auth      *api.AuthHandler
	Knowledge *api.KnowledgeHandler
	Quiz      *api.QuizHandler
	JWTSecret string
}

// NewRouter 组装 Engine + 全局中间件，并按模块聚合路由。
func NewRouter(log *zap.Logger, deps Deps) *Router {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recover(log), middleware.Logger(log))

	r.GET("/health", func(c *gin.Context) {
		resp.OK(c, gin.H{"status": "up"})
	})

	registerAuth(r, deps)
	registerKnowledge(r, deps)
	registerQuiz(r, deps)

	return &Router{engine: r, log: log}
}

func (r *Router) Engine() *gin.Engine { return r.engine }

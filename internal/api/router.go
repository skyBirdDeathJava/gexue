// Package api Gin 路由注册
package api

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

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
	Auth      *AuthHandler
	Knowledge *KnowledgeHandler
	JWTSecret string
}

func NewRouter(log *zap.Logger, deps Deps) *Router {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recover(log), middleware.Logger(log))

	r.GET("/health", func(c *gin.Context) {
		resp.OK(c, gin.H{"status": "up"})
	})

	// 认证（公开）
	if deps.Auth != nil {
		auth := r.Group("/api/auth")
		auth.POST("/register", deps.Auth.Register)
		auth.POST("/login", deps.Auth.Login)
		auth.GET("/me", middleware.AuthRequired(deps.JWTSecret), deps.Auth.Me)
	}

	// 知识库（需登录）
	if deps.Knowledge != nil {
		kb := r.Group("/api")
		kb.Use(middleware.AuthRequired(deps.JWTSecret))
		deps.Knowledge.Register(kb)
	}

	return &Router{engine: r, log: log}
}

func (r *Router) Engine() *gin.Engine { return r.engine }

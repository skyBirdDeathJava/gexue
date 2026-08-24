package route

import (
	"github.com/gin-gonic/gin"

	"gexue/internal/api/middleware"
)

// registerAuth 认证模块路由（公开；/me 需登录）。
func registerAuth(r *gin.Engine, deps Deps) {
	if deps.Auth == nil {
		return
	}
	auth := r.Group("/api/auth")
	auth.POST("/send-code", deps.Auth.SendCode)
	auth.POST("/login-by-phone", deps.Auth.LoginByPhone)
	auth.GET("/me", middleware.AuthRequired(deps.JWTSecret), deps.Auth.Me)
}

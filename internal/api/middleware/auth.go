package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gexue/internal/pkg/jwtutil"
)

// AuthRequired 从 Authorization: Bearer <token> 解析 userID 注入上下文。
func AuthRequired(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1001, "message": "未登录"})
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth {
			token = auth
		}
		userID, err := jwtutil.Parse(secret, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1001, "message": "Token无效或已过期"})
			return
		}
		c.Set("user_id", userID)
		c.Next()
	}
}

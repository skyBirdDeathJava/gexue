// Package middleware 通用中间件：请求ID、恢复、日志
package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const RequestIDHeader = "X-Request-ID"

// RequestID 为每个请求注入 X-Request-ID（透传已有值）
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(RequestIDHeader)
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set("request_id", rid)
		c.Header(RequestIDHeader, rid)
		c.Next()
	}
}

// Recover 捕获 panic 并记录日志，避免进程崩溃
func Recover(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("panic recovered",
					zap.Any("error", err),
					zap.String("request_id", c.GetString("request_id")),
					zap.String("path", c.Request.URL.Path),
				)
				c.AbortWithStatusJSON(500, gin.H{"code": 5000, "message": "internal error"})
			}
		}()
		c.Next()
	}
}

// Logger 请求日志：方法、路径、状态码、耗时
func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http request",
			zap.String("request_id", c.GetString("request_id")),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}

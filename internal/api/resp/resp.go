// Package resp 统一响应封装：{code, message, data}
package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 业务错误码：0 成功；1000+ 业务错误
const (
	CodeOK           = 0
	CodeBadRequest   = 1000
	CodeUnauthorized = 1001
	CodeNotFound     = 1002
	CodeInternal     = 5000
)

type Body struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Body{Code: CodeOK, Message: "ok", Data: data})
}

func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Body{Code: code, Message: msg})
}

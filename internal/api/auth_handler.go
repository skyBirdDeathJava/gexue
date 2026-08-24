package api

import (
	"github.com/gin-gonic/gin"

	"gexue/internal/api/resp"
	"gexue/internal/service"
)

type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(svc *service.AuthService) *AuthHandler { return &AuthHandler{svc: svc} }

type sendCodeReq struct {
	Phone string `json:"phone" binding:"required"`
}

type phoneAuthReq struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// SendCode 发送登录验证码
func (h *AuthHandler) SendCode(c *gin.Context) {
	var in sendCodeReq
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if err := h.svc.SendVerifyCode(c.Request.Context(), in.Phone); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, gin.H{"message": "验证码已发送"})
}

// LoginByPhone 手机号 + 验证码登录，首次自动注册
func (h *AuthHandler) LoginByPhone(c *gin.Context) {
	var in phoneAuthReq
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	token, u, isNew, err := h.svc.LoginByPhone(c.Request.Context(), in.Phone, in.Code)
	if err != nil {
		if err == service.ErrInvalidCode {
			resp.Fail(c, resp.CodeBadRequest, "验证码错误或已过期")
			return
		}
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, gin.H{"token": token, "user": u, "is_new": isNew})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID := c.GetUint("user_id")
	resp.OK(c, gin.H{"user_id": userID})
}

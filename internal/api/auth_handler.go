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

type authReq struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var in authReq
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	u, err := h.svc.Register(c.Request.Context(), in.Phone, in.Password)
	if err != nil {
		if err == service.ErrPhoneExists {
			resp.Fail(c, resp.CodeBadRequest, "手机号已注册")
			return
		}
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, u)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var in authReq
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	token, u, err := h.svc.Login(c.Request.Context(), in.Phone, in.Password)
	if err != nil {
		if err == service.ErrBadLogin {
			resp.Fail(c, resp.CodeUnauthorized, "手机号或密码错误")
			return
		}
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, gin.H{"token": token, "user": u})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID := c.GetUint("user_id")
	// 通过 AuthService 暴露的查询（此处简化为返回 user_id；完整信息可由 UserRepo 补）
	resp.OK(c, gin.H{"user_id": userID})
}

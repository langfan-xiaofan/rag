package handler

import (
	"rag/internal/dto"
	"rag/internal/service"
	"rag/utils/jwt"
	"rag/utils/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{
		svc: service.NewUserService(db),
	}
}

func (h *UserHandler) Register(c *gin.Context) {
	var req dto.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, nil, "参数错误")
		return
	}
	if err := h.svc.Register(c, req); err != nil {
		c.JSON(500, map[string]any{
			"msg":  "注册失败" + err.Error(),
			"code": 500,
			"data": nil,
		})
		return
	}
	c.JSON(200, map[string]any{
		"msg":  "注册成功",
		"data": nil,
		"code": 200,
	})
}

func (h *UserHandler) Login(c *gin.Context) {
	var req dto.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, map[string]any{
			"msg":  "参数错误",
			"data": nil,
		})
		return
	}
	err := h.svc.Login(c, req)
	if err != nil {
		c.JSON(500, map[string]any{
			"msg":  err.Error(),
			"data": nil,
		})
		return
	}
	token, err := jwt.GenerateToken(req.Username)
	c.JSON(200, map[string]any{
		"msg": "登录成功",
		"data": map[string]any{
			"token": token,
		},
	})
}

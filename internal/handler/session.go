package handler

import (
	"errors"
	"net/http"
	"strings"

	"rag/internal/dto"
	"rag/internal/service"
	"rag/utils/response"

	"github.com/gin-gonic/gin"
)

type SessionHandler struct {
	svc *service.SessionService
}

func NewSessionHandler(svc *service.SessionService) *SessionHandler {
	return &SessionHandler{svc: svc}
}

func (h *SessionHandler) Create(c *gin.Context) {
	var req dto.CreateSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, nil, "参数错误: "+err.Error())
		return
	}
	res, err := h.svc.Create(c.Request.Context(), c.GetUint("user_id"), req.KnowledgeBaseID)
	if err != nil {
		failSession(c, err)
		return
	}
	response.Success(c, res)
}

func (h *SessionHandler) List(c *gin.Context) {
	res, err := h.svc.List(c.Request.Context(), c.GetUint("user_id"))
	if err != nil {
		failSession(c, err)
		return
	}
	response.Success(c, res)
}

func (h *SessionHandler) Messages(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		response.Fail(c, http.StatusBadRequest, nil, "会话 ID 不能为空")
		return
	}
	res, err := h.svc.Messages(c.Request.Context(), c.GetUint("user_id"), sessionID)
	if err != nil {
		failSession(c, err)
		return
	}
	response.Success(c, res)
}

func (h *SessionHandler) Delete(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		response.Fail(c, http.StatusBadRequest, nil, "会话 ID 不能为空")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), c.GetUint("user_id"), sessionID); err != nil {
		failSession(c, err)
		return
	}
	response.Success(c, nil)
}

func failSession(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSessionNotFound),
		errors.Is(err, service.ErrKnowledgeBaseNotFound):
		response.Fail(c, http.StatusNotFound, nil, err.Error())
	default:
		response.Fail(c, http.StatusInternalServerError, nil, err.Error())
	}
}

package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rag/internal/dto"
	"rag/internal/service"
	"rag/utils/response"

	"github.com/gin-gonic/gin"
)

type KnowledgeBaseHandler struct {
	svc *service.KnowledgeBaseService
}

func NewKnowledgeBaseHandler(svc *service.KnowledgeBaseService) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{svc: svc}
}

func (h *KnowledgeBaseHandler) Create(c *gin.Context) {
	var req dto.CreateKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, nil, "参数错误: "+err.Error())
		return
	}
	res, err := h.svc.Create(c.Request.Context(), c.GetUint("user_id"), req)
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, res)
}

func (h *KnowledgeBaseHandler) List(c *gin.Context) {
	res, err := h.svc.List(c.Request.Context(), c.GetUint("user_id"))
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, res)
}

func (h *KnowledgeBaseHandler) Update(c *gin.Context) {
	kbID, ok := parseKnowledgeBaseID(c)
	if !ok {
		return
	}
	var req dto.UpdateKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, nil, "参数错误: "+err.Error())
		return
	}
	res, err := h.svc.Update(c.Request.Context(), c.GetUint("user_id"), kbID, req)
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, res)
}

func (h *KnowledgeBaseHandler) Delete(c *gin.Context) {
	kbID, ok := parseKnowledgeBaseID(c)
	if !ok {
		return
	}
	err := h.svc.Delete(c.Request.Context(), c.GetUint("user_id"), c.GetString("username"), kbID)
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeBaseHandler) ListFiles(c *gin.Context) {
	kbID, ok := parseKnowledgeBaseID(c)
	if !ok {
		return
	}
	res, err := h.svc.ListFiles(c.Request.Context(), c.GetUint("user_id"), kbID)
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, res)
}

func (h *KnowledgeBaseHandler) DeleteFile(c *gin.Context) {
	kbID, ok := parseKnowledgeBaseID(c)
	if !ok {
		return
	}
	fileName := strings.TrimSpace(c.Query("file_name"))
	if fileName == "" {
		var req struct {
			FileName string `json:"file_name"`
		}
		_ = c.ShouldBindJSON(&req)
		fileName = strings.TrimSpace(req.FileName)
	}
	if fileName == "" {
		response.Fail(c, http.StatusBadRequest, nil, "缺少 file_name")
		return
	}
	err := h.svc.DeleteFile(c.Request.Context(), c.GetUint("user_id"), c.GetString("username"), kbID, fileName)
	if err != nil {
		failKnowledgeBase(c, err)
		return
	}
	response.Success(c, nil)
}

func parseKnowledgeBaseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.Fail(c, http.StatusBadRequest, nil, "知识库 ID 不合法")
		return 0, false
	}
	return uint(id), true
}

func failKnowledgeBase(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeBaseNotFound):
		response.Fail(c, http.StatusNotFound, nil, err.Error())
	case errors.Is(err, service.ErrKnowledgeBaseDuplicate),
		errors.Is(err, service.ErrKnowledgeBaseNameEmpty),
		errors.Is(err, service.ErrFileNameEmpty):
		response.Fail(c, http.StatusBadRequest, nil, err.Error())
	default:
		response.Fail(c, http.StatusInternalServerError, nil, err.Error())
	}
}

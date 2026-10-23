package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rag/internal/ingest"
	"rag/internal/service"
	"rag/utils/response"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	svc *service.FileService
}

func NewFileHandler(pipeline *ingest.Pipeline, kbSvc *service.KnowledgeBaseService) *FileHandler {
	return &FileHandler{
		svc: service.NewFileService(pipeline, kbSvc),
	}
}

// UpLoadFile 接收 multipart 表单，交给 service 批量索引入库。
// kb_id 必填且必须属于当前用户；只要有一个文件失败就返回失败列表，其余文件照常处理。
func (h *FileHandler) UpLoadFile(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		response.Fail(c, http.StatusBadRequest, nil, "解析表单失败: "+err.Error())
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		response.Fail(c, http.StatusBadRequest, nil, "缺少文件字段 files")
		return
	}
	kbID, err := strconv.ParseUint(strings.TrimSpace(c.PostForm("kb_id")), 10, 64)
	if err != nil || kbID == 0 {
		response.Fail(c, http.StatusBadRequest, nil, "缺少或非法的 kb_id")
		return
	}
	// filePrefix 可选：给了就存成 知识库前缀/prefix/filename。
	// 真实 key 会随文件记录一起落库，get_files 签发下载链接时按记录里的 key 走。
	prefix := strings.TrimSpace(c.PostForm("filePrefix"))

	failed, err := h.svc.Upload(c.Request.Context(), files,
		c.GetUint("user_id"), c.GetString("username"), uint(kbID), prefix)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrKnowledgeBaseNotFound) {
			status = http.StatusNotFound
		}
		response.Fail(c, status, nil, err.Error())
		return
	}
	if len(failed) > 0 {
		response.Fail(c, http.StatusInternalServerError, failed, "文件上传失败")
		return
	}
	c.JSON(http.StatusAccepted, map[string]any{
		"msg":  "文件上传完毕",
		"data": nil,
	})
}

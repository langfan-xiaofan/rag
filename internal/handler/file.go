package handler

import (
	"net/http"
	"strings"

	"rag/internal/ingest"
	"rag/internal/service"
	"rag/utils/response"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	svc *service.FileService
}

func NewFileHandler(pipeline *ingest.Pipeline) *FileHandler {
	return &FileHandler{
		svc: service.NewFileService(pipeline),
	}
}

// UpLoadFile 接收 multipart 表单，交给 service 批量索引入库。
// 只要有一个文件失败就返回失败列表，其余文件照常处理。
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
	// filePrefix 可选：给了就存成 prefix/filename，不给就直接放在桶根目录。
	// 真实 key 会随文件记录一起落库，get_files 签发下载链接时按记录里的 key 走，
	// 所以填不填前缀都能下载到（早期没有 key 的记录再按文件名在桶里兜底找）。
	prefix := strings.TrimSpace(c.PostForm("filePrefix"))
	// collectionName 只做校验，不参与逻辑：集合名固定取 username，
	// 让客户端指定集合名会造成跨用户数据串写。
	if len(form.Value["collectionName"]) == 0 {
		response.Fail(c, http.StatusBadRequest, nil, "缺少collectionName字段")
		return
	}

	failed := h.svc.Upload(c.Request.Context(), files, c.GetString("username"), prefix)
	if len(failed) > 0 {
		response.Fail(c, 500, failed, "文件上传失败")
		return
	}
	c.JSON(http.StatusAccepted, map[string]any{
		"msg":  "文件上传完毕",
		"data": nil,
	})
}

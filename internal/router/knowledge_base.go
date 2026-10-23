package router

import (
	"rag/internal/handler"
	"rag/internal/service"

	"github.com/gin-gonic/gin"
)

func InitKnowledgeBaseRouter(r *gin.RouterGroup, svc *service.KnowledgeBaseService) {
	h := handler.NewKnowledgeBaseHandler(svc)
	r.POST("", h.Create)
	r.GET("", h.List)
	r.PUT("/:id", h.Update)
	r.DELETE("/:id", h.Delete)
	r.GET("/:id/files", h.ListFiles)
	r.DELETE("/:id/files", h.DeleteFile)
}

package router

import (
	"rag/internal/handler"
	"rag/internal/ingest"
	"rag/internal/service"

	"github.com/gin-gonic/gin"
)

func InitFileRouter(r *gin.RouterGroup, pipeline *ingest.Pipeline, kbSvc *service.KnowledgeBaseService) {
	fileHandler := handler.NewFileHandler(pipeline, kbSvc)
	r.POST("/upload", fileHandler.UpLoadFile)
}

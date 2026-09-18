package router

import (
	"rag/internal/handler"
	"rag/internal/ingest"

	"github.com/gin-gonic/gin"
)

func InitFileRouter(r *gin.RouterGroup, pipeline *ingest.Pipeline) {
	FileHandler := handler.NewFileHandler(pipeline)
	r.POST("/upload", FileHandler.UpLoadFile)
}

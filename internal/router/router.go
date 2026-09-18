package router

import (
	"rag/internal/ingest"
	"rag/internal/middleware"
	"rag/internal/silo"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/gin-gonic/gin"
	"github.com/qdrant/go-client/qdrant"
	"gorm.io/gorm"
)

func InitRouter(r *gin.Engine, db *gorm.DB, qdrantClient *qdrant.Client, siloClient *silo.Silo, pipeline *ingest.Pipeline, Textembedder embedding.Embedder, chatmodel *openai.ChatModel) error {
	fileGroup := r.Group("/v1/file", middleware.AuthMiddleware())
	InitFileRouter(fileGroup, pipeline)

	agentGroup := r.Group("/v1/agent", middleware.AuthMiddleware())
	InitAgentRouter(agentGroup, Textembedder, qdrantClient, chatmodel, siloClient)

	userGroup := r.Group("/v1/user")
	InitUserRouter(userGroup, db)
	return nil
}

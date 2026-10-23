package router

import (
	"rag/internal/ingest"
	"rag/internal/memory"
	"rag/internal/middleware"
	"rag/internal/service"
	"rag/internal/silo"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/gin-gonic/gin"
	"github.com/qdrant/go-client/qdrant"
	"gorm.io/gorm"
)

func InitRouter(r *gin.Engine, db *gorm.DB, qdrantClient *qdrant.Client,
	siloClient *silo.Silo, pipeline *ingest.Pipeline, sessionManager *memory.SessionManager,
	Textembedder embedding.Embedder, chatmodel *openai.ChatModel,
	kbSvc *service.KnowledgeBaseService) error {
	kbGroup := r.Group("/v1/kb", middleware.AuthMiddleware())
	InitKnowledgeBaseRouter(kbGroup, kbSvc)

	fileGroup := r.Group("/v1/file", middleware.AuthMiddleware())
	InitFileRouter(fileGroup, pipeline, kbSvc)

	agentGroup := r.Group("/v1/agent", middleware.AuthMiddleware())
	InitAgentRouter(agentGroup, Textembedder, qdrantClient, chatmodel, siloClient, sessionManager, kbSvc)

	sessionGroup := r.Group("/v1/session", middleware.AuthMiddleware())
	InitSessionRouter(sessionGroup, service.NewSessionService(db, sessionManager, kbSvc))

	userGroup := r.Group("/v1/user")
	InitUserRouter(userGroup, db)
	return nil
}

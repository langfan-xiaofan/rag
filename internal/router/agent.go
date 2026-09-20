package router

import (
	"rag/internal/handler"
	"rag/internal/memory"
	"rag/internal/silo"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/gin-gonic/gin"
	"github.com/qdrant/go-client/qdrant"
)

func InitAgentRouter(r *gin.RouterGroup, embedder embedding.Embedder, qdrant *qdrant.Client, chatmodel *openai.ChatModel, siloClient *silo.Silo, sessionManager *memory.SessionManager) {
	AgentHandler := handler.NewAgentHandler(qdrant, embedder, chatmodel, siloClient, sessionManager)
	r.POST("/ask", AgentHandler.Ask)
}

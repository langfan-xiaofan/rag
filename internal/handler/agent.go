package handler

import (
	"fmt"
	"net/http"
	"rag/internal/dto"
	"rag/internal/service"
	"rag/internal/silo"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/gin-gonic/gin"
	"github.com/qdrant/go-client/qdrant"
)

type AgentHnadler struct {
	svc *service.AgentService
}

func NewAgentHandler(qdrant *qdrant.Client, embedder embedding.Embedder, chatmodel *openai.ChatModel, siloClient *silo.Silo) *AgentHnadler {
	return &AgentHnadler{
		service.NewAgentService(qdrant, embedder, chatmodel, siloClient),
	}
}

func (h *AgentHnadler) Ask(c *gin.Context) {
	var req dto.AskReq
	username := c.GetString("username")
	fmt.Println("username:", username)
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.JSON(500, map[string]any{
			"msg":  "参数错误" + err.Error(),
			"data": nil,
		})
		return
	}
	ch, err := h.svc.Ask(req.Query, username, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{
			"msg":  "生成回答错误" + err.Error(),
			"data": nil,
		})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	for chunk := range ch {
		fmt.Println(chunk)
		if chunk.Err == nil {
			c.SSEvent("message", chunk.Content)
		}
		c.Writer.Flush()
	}
	c.SSEvent("done", "[DONE]")
	c.Writer.Flush()
}

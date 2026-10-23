package handler

import (
	"errors"
	"net/http"
	"rag/internal/dto"
	"rag/internal/memory"
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

func NewAgentHandler(qdrant *qdrant.Client, embedder embedding.Embedder, chatmodel *openai.ChatModel,
	siloClient *silo.Silo, sessionManager *memory.SessionManager, kbSvc *service.KnowledgeBaseService) *AgentHnadler {
	return &AgentHnadler{
		service.NewAgentService(qdrant, embedder, chatmodel, siloClient, sessionManager, kbSvc),
	}
}

func (h *AgentHnadler) Ask(c *gin.Context) {
	var req dto.AskReq
	username := c.GetString("username")
	userID := c.GetUint("user_id")
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{
			"msg":  "参数错误: " + err.Error(),
			"data": nil,
		})
		return
	}
	sessionID, ch, err := h.svc.Ask(c.Request.Context(), req.Query, req.KnowledgeBaseID,
		username, 10, userID, req.SessionID, 20)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, memory.ErrSessionDeleted):
			status = http.StatusBadRequest
		case errors.Is(err, memory.ErrSessionKBConflict):
			status = http.StatusConflict
		case errors.Is(err, service.ErrKnowledgeBaseNotFound):
			status = http.StatusNotFound
		}
		c.JSON(status, map[string]any{
			"msg":  "生成回答错误: " + err.Error(),
			"data": nil,
		})
		return
	}
	// 新对话的 session_id 在这里回给前端，下一轮请求带上它即可续上历史
	c.Header("session_id", sessionID)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	for chunk := range ch {
		if chunk.Err != nil {
			c.SSEvent("error", chunk.Err.Error())
			c.Writer.Flush()
			continue
		}
		c.SSEvent("message", chunk.Content)
		c.Writer.Flush()
	}
	c.SSEvent("done", "[DONE]")
	c.Writer.Flush()
}

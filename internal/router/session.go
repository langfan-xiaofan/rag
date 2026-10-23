package router

import (
	"rag/internal/handler"
	"rag/internal/service"

	"github.com/gin-gonic/gin"
)

func InitSessionRouter(r *gin.RouterGroup, svc *service.SessionService) {
	h := handler.NewSessionHandler(svc)
	r.POST("", h.Create)
	r.GET("", h.List)
	r.GET("/:id/messages", h.Messages)
	r.DELETE("/:id", h.Delete)
}

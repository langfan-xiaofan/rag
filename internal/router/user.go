package router

import (
	"rag/internal/handler"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func InitUserRouter(r *gin.RouterGroup, db *gorm.DB) {
	handler := handler.NewUserHandler(db)
	r.POST("/register", handler.Register)
	r.POST("/login", handler.Login)
}

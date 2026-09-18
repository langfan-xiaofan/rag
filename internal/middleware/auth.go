package middleware

import (
	"rag/utils/jwt"
	"rag/utils/response"
	"strings"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Fail(c, 401, nil, "未携带token")
			c.Abort()
			return
		}
		authHeader = strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := jwt.ParseToken(authHeader)
		if err != nil {
			response.Fail(c, 401, nil, "Token无效或已过期")
			c.Abort()
			return
		}
		c.Set("username", claims.Username)
		c.Next()
	}
}

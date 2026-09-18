package response

import "github.com/gin-gonic/gin"

type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

func Success(c *gin.Context, data interface{}) {
	c.JSON(200, Response{
		Code: 200,
		Msg:  "success",
		Data: data,
	})
}

func Fail(c *gin.Context, code int, data interface{}, message string) {
	c.JSON(code, Response{
		Code: code,
		Msg:  message,
		Data: data,
	})
}

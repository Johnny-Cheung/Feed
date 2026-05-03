package response

import (
	"errors"
	"net/http"

	appErrors "feed-backend/internal/common/errors"

	"github.com/gin-gonic/gin"
)

// envelope 是统一响应外层结构。
// 所有接口最终都应该返回这种格式，方便前端统一处理。
type envelope struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// Success 用于返回统一的成功响应。
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, envelope{
		Code:    appErrors.CodeSuccess,
		Message: "ok",
		Data:    data,
	})
}

// Error 用于返回统一的错误响应。
// 这里会优先识别我们自己的 AppError，
// 如果不是，就退化成一个通用的 500。
func Error(c *gin.Context, err error) {
	var appErr *appErrors.AppError
	if errors.As(err, &appErr) {
		c.JSON(appErr.HTTPStatus, envelope{
			Code:    appErr.Code,
			Message: appErr.Message,
			Data:    nil,
		})
		return
	}

	c.JSON(http.StatusInternalServerError, envelope{
		Code:    appErrors.CodeInternalError,
		Message: "internal server error",
		Data:    nil,
	})
}

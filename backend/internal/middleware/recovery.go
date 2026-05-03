package middleware

import (
	"log"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// Recovery 中间件用于兜底捕获 panic。
// 这样即使某个请求内部发生了 panic，也不会把整个服务打崩。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				// 先把 panic 打到日志里，方便排查。
				log.Printf("panic recovered: %v", rec)

				// 再返回统一的 500 响应给客户端。
				response.Error(c, appErrors.Internal(nil))
				c.Abort()
			}
		}()

		// 继续执行后续中间件和业务处理函数。
		c.Next()
	}
}

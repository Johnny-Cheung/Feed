package middleware

import (
	"net/http"

	"feed-backend/internal/bootstrap"

	"github.com/gin-gonic/gin"
)

// CORS 中间件处理浏览器跨域请求。
// 当前实现是“白名单模式”：
// 只有配置中允许的 Origin 才会被放行。
func CORS(cfg bootstrap.CORSConfig) gin.HandlerFunc {
	// 先把允许的来源放进 map，后续查询效率更高。
	allowedOrigins := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, origin := range cfg.AllowOrigins {
		allowedOrigins[origin] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			// 只有命中的 Origin 才设置 Allow-Origin。
			if _, ok := allowedOrigins[origin]; ok {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)

				// Vary: Origin 告诉缓存系统：
				// 不同 Origin 的响应不能简单共用缓存。
				c.Writer.Header().Set("Vary", "Origin")
			}

			// 这几项是浏览器做跨域校验时常用的头。
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// 预检请求（OPTIONS）不需要进入实际业务处理，直接返回 204 即可。
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

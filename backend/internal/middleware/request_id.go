package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// RequestIDKey 是把请求 ID 存进 Gin Context 时使用的 key。
const RequestIDKey = "request_id"

// RequestID 中间件为每个请求分配一个请求 ID。
// 请求 ID 的作用：
// 1. 方便日志排查
// 2. 方便前后端串联一次请求
// 3. 方便以后接入链路追踪
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果客户端自己传了 X-Request-ID，就优先复用。
		// 这样跨服务调用时可以把同一个请求 ID 继续传下去。
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
		}

		// 一份放到上下文里给后续代码读取，
		// 一份写回响应头，方便客户端和日志系统看到。
		c.Set(RequestIDKey, requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)
		c.Next()
	}
}

// newRequestID 生成一个随机请求 ID。
// 这里使用 16 字节随机数，再编码成十六进制字符串。
func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 理论上这里极少失败。
		// 如果失败，退化成一个固定值，保证请求流程还能继续。
		return "request-id-unavailable"
	}
	return hex.EncodeToString(buf)
}

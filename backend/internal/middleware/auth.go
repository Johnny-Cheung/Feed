package middleware

import (
	"strings"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"

	"github.com/gin-gonic/gin"
)

// Auth 要求请求必须带合法 access token。
// 只要 token 缺失、格式错误、解析失败，都会直接返回 401。
func Auth(tokenManager *authDomain.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, ok := extractBearerToken(c.GetHeader("Authorization"))
		if !ok {
			response.Error(c, appErrors.ErrUnauthorized)
			c.Abort()
			return
		}

		claims, err := tokenManager.ParseAccessToken(tokenString)
		if err != nil {
			response.Error(c, appErrors.ErrUnauthorized)
			c.Abort()
			return
		}

		c.Set(authDomain.CurrentUserIDContextKey, claims.UserID)
		c.Next()
	}
}

// extractBearerToken 从 Authorization 头中提取 Bearer token。
// 合法格式必须是：
// Authorization: Bearer <token>
func extractBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "", false
	}

	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}

	return token, true
}

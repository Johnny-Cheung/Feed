package middleware

import (
	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"

	"github.com/gin-gonic/gin"
)

// OptionalAuth 允许匿名访问，但如果带了 token，就必须是合法 token。
// 它适合后面首页、作者主页这种“未登录也能看，但登录后想返回更多状态”的接口。
func OptionalAuth(tokenManager *authDomain.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.Next()
			return
		}

		tokenString, ok := extractBearerToken(header)
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

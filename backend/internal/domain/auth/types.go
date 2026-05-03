package auth

import "github.com/gin-gonic/gin"

// CurrentUserIDContextKey 是把当前登录用户 ID 写入 Gin Context 时使用的 key。
const CurrentUserIDContextKey = "current_user_id"

// RegisterRequest 是注册接口的请求体。
// 当前阶段只需要用户名和密码两个字段。
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=4,max=32"`
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// LoginRequest 是登录接口的请求体。
type LoginRequest struct {
	Username string `json:"username" binding:"required,min=4,max=32"`
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// TokenResponse 是注册成功或登录成功后的响应体。
// 前端拿到 access_token 后，后续请求需要放进 Authorization 头里。
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// CurrentUserResponse 是“当前登录用户信息”接口的响应体。
type CurrentUserResponse struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
}

// CurrentUserIDFromContext 从 Gin Context 中读取当前登录用户 ID。
func CurrentUserIDFromContext(c *gin.Context) (uint64, bool) {
	value, exists := c.Get(CurrentUserIDContextKey)
	if !exists {
		return 0, false
	}

	userID, ok := value.(uint64)
	return userID, ok
}

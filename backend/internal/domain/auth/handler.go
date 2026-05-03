package auth

import (
	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// Handler 负责处理认证相关 HTTP 请求。
type Handler struct {
	service *Service
}

// NewHandler 创建认证 Handler。
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Register 处理注册请求。
func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.Register(c.Request.Context(), req)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Login 处理登录请求。
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.Login(c.Request.Context(), req)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Me 返回当前登录用户信息。
func (h *Handler) Me(c *gin.Context) {
	userID, ok := CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	resp, err := h.service.GetCurrentUser(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

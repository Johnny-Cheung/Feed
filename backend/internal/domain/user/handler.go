package user

import (
	"net/http"
	"strconv"
	"strings"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) DispatchGET(c *gin.Context) {
	path := strings.Trim(c.Param("path"), "/")
	switch {
	case path == "me":
		h.Me(c)
	case path == "me/videos":
		h.MyVideos(c)
	case path == "me/liked-videos":
		h.MyLikedVideos(c)
	case path == "me/favorited-videos":
		h.MyFavoritedVideos(c)
	case path == "me/followings":
		h.MyFollowings(c)
	case path == "me/followers":
		h.MyFollowers(c)
	case path != "" && !strings.Contains(path, "/"):
		h.AuthorProfile(c, path)
	case strings.HasSuffix(path, "/videos") && strings.Count(path, "/") == 1:
		h.AuthorVideos(c, strings.TrimSuffix(path, "/videos"))
	default:
		response.Error(c, appErrors.ErrNotFound)
	}
}

func (h *Handler) Me(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	resp, err := h.service.GetMe(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.UpdateProfile(c.Request.Context(), userID, req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UpdateAvatar(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	avatarHeader, err := c.FormFile("avatar")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.UpdateAvatar(c.Request.Context(), userID, avatarHeader)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UpdatePassword(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req UpdatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	if err := h.service.UpdatePassword(c.Request.Context(), userID, req); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *Handler) MyVideos(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}

	resp, err := h.service.ListMyVideos(c.Request.Context(), userID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) MyLikedVideos(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}

	resp, err := h.service.ListLikedVideos(c.Request.Context(), userID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) MyFavoritedVideos(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}

	resp, err := h.service.ListFavoritedVideos(c.Request.Context(), userID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) MyFollowings(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}

	resp, err := h.service.ListFollowings(c.Request.Context(), userID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) MyFollowers(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}

	resp, err := h.service.ListFollowers(c.Request.Context(), userID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) AuthorProfile(c *gin.Context, rawUserID string) {
	targetUserID, ok := parseUserID(c, rawUserID)
	if !ok {
		return
	}
	viewerUserID, _ := authDomain.CurrentUserIDFromContext(c)

	resp, err := h.service.GetAuthorProfile(c.Request.Context(), viewerUserID, targetUserID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) AuthorVideos(c *gin.Context, rawUserID string) {
	targetUserID, ok := parseUserID(c, rawUserID)
	if !ok {
		return
	}
	requestedLimit, ok := parseRequestedLimit(c)
	if !ok {
		return
	}
	viewerUserID, _ := authDomain.CurrentUserIDFromContext(c)

	resp, err := h.service.ListAuthorVideos(c.Request.Context(), viewerUserID, targetUserID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func currentUserID(c *gin.Context) (uint64, bool) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return 0, false
	}
	return userID, true
}

func parseRequestedLimit(c *gin.Context) (int, bool) {
	rawLimit := c.Query("limit")
	if rawLimit == "" {
		return 0, true
	}

	requestedLimit, err := strconv.Atoi(rawLimit)
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return 0, false
	}
	return requestedLimit, true
}

func parseUserID(c *gin.Context, raw string) (uint64, bool) {
	userID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || userID == 0 {
		response.Error(c, appErrors.ErrInvalidParams)
		return 0, false
	}
	return userID, true
}

func methodNotAllowed(c *gin.Context) {
	c.JSON(http.StatusMethodNotAllowed, gin.H{
		"code":    appErrors.CodeInvalidParams,
		"message": "method not allowed",
		"data":    nil,
	})
}

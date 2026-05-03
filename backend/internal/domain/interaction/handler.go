package interaction

import (
	"errors"
	"strconv"

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

func (h *Handler) LikeVideo(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.LikeVideo(c.Request.Context(), userID, videoID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UnlikeVideo(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.UnlikeVideo(c.Request.Context(), userID, videoID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) FavoriteVideo(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.FavoriteVideo(c.Request.Context(), userID, videoID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UnfavoriteVideo(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.UnfavoriteVideo(c.Request.Context(), userID, videoID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) FollowUser(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	targetUserID, err := parseUintPathParam(c.Param("user_id"), "user id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.FollowUser(c.Request.Context(), userID, targetUserID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) UnfollowUser(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	targetUserID, err := parseUintPathParam(c.Param("user_id"), "user id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.UnfollowUser(c.Request.Context(), userID, targetUserID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) CreateComment(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	var req CreateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.CreateComment(c.Request.Context(), userID, videoID, req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	userID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	commentID, err := parseUintPathParam(c.Param("comment_id"), "comment id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	if err := h.service.DeleteComment(c.Request.Context(), userID, commentID); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *Handler) ListComments(c *gin.Context) {
	videoID, err := parseUintPathParam(c.Param("video_id"), "video id")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	requestedLimit := 0
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsedLimit, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil {
			response.Error(c, appErrors.ErrInvalidParams)
			return
		}
		requestedLimit = parsedLimit
	}

	resp, err := h.service.ListComments(c.Request.Context(), videoID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

func parseUintPathParam(raw string, field string) (uint64, error) {
	if raw == "" {
		return 0, errors.New(field + " is empty")
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, errors.New(field + " is invalid")
	}
	return value, nil
}

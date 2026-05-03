package video

import (
	"errors"
	"net/http"
	"strconv"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"

	"github.com/gin-gonic/gin"
)

// Handler 负责处理视频领域相关 HTTP 请求。
// 教学理解：
// Handler 只做“HTTP 世界”的工作，例如：
// - 读路径参数
// - 读表单字段
// - 取当前登录用户
// - 把业务结果写回 JSON
//
// 真正的业务规则不要堆在这里，而是交给 Service。
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Publish 处理视频发布接口。
func (h *Handler) Publish(c *gin.Context) {
	// 对发布接口来说，当前用户 ID 不是从请求体里传的，
	// 而是从鉴权中间件提前写进 Gin Context 的。
	operatorUserID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoHeader, err := c.FormFile("video")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	coverHeader, err := c.FormFile("cover")
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	req := PublishRequest{
		Title:       c.PostForm("title"),
		VideoHeader: videoHeader,
		CoverHeader: coverHeader,
	}

	// 到这里，HTTP 细节已经处理完了。
	// 接下来把它交给 Service，让 Service 去做真正的发布流程编排。
	resp, err := h.service.Publish(c.Request.Context(), operatorUserID, req)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Detail 处理视频详情接口。
func (h *Handler) Detail(c *gin.Context) {
	// 路径参数都是字符串，所以先要转成 uint64。
	videoID, err := parseVideoID(c.Param("video_id"))
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	viewerUserID, _ := authDomain.CurrentUserIDFromContext(c)
	resp, err := h.service.GetDetail(c.Request.Context(), videoID, viewerUserID)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Update 处理视频编辑接口。
func (h *Handler) Update(c *gin.Context) {
	operatorUserID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseVideoID(c.Param("video_id"))
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	var req UpdateRequest
	if title, exists := c.GetPostForm("title"); exists {
		// 只要 title 字段出现了，就认为客户端想更新标题。
		// 即使它的值是空字符串，也要交给 Service 去做长度校验。
		req.Title = &title
	}

	coverHeader, err := c.FormFile("cover")
	if err == nil {
		req.CoverHeader = coverHeader
	} else if !errors.Is(err, http.ErrMissingFile) {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	resp, err := h.service.Update(c.Request.Context(), operatorUserID, videoID, req)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Delete 处理视频删除接口。
func (h *Handler) Delete(c *gin.Context) {
	operatorUserID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	videoID, err := parseVideoID(c.Param("video_id"))
	if err != nil {
		response.Error(c, appErrors.ErrInvalidParams)
		return
	}

	if err := h.service.Delete(c.Request.Context(), operatorUserID, videoID); err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

func parseVideoID(raw string) (uint64, error) {
	// 把 URL 中的字符串参数转换成真正可用于数据库查询的数值 ID。
	if raw == "" {
		return 0, errors.New("video id is empty")
	}

	videoID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || videoID == 0 {
		return 0, errors.New("video id is invalid")
	}

	return videoID, nil
}

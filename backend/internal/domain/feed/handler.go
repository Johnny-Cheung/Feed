package feed

import (
	"strconv"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"

	"github.com/gin-gonic/gin"
)

// Handler 负责处理 Feed 领域相关 HTTP 请求。
// 和其他领域一样，Handler 只负责 HTTP 细节：
// - 取 query 参数
// - 取当前登录用户
// - 调 Service
// - 返回统一 JSON
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func parseRequestedLimit(c *gin.Context) (int, error) {
	var requestedLimit int
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return 0, appErrors.ErrInvalidParams
		}
		requestedLimit = parsedLimit
	}

	return requestedLimit, nil
}

// Home 处理 GET /api/v1/feed/home。
// 这是第七阶段首页热榜流的 HTTP 入口。
func (h *Handler) Home(c *gin.Context) {
	// limit 是 query 参数，拿出来时还是字符串。
	// 这里先转成 int，再交给 Service 去做默认值和最大值限制。
	requestedLimit, err := parseRequestedLimit(c)
	if err != nil {
		response.Error(c, err)
		return
	}

	// 首页允许匿名访问，所以这里即使拿不到用户 ID 也不是错误。
	// 匿名访问时 viewerUserID 会保持为 0，后面 Service 会按匿名用户处理。
	viewerUserID, _ := authDomain.CurrentUserIDFromContext(c)
	resp, err := h.service.GetHome(c.Request.Context(), viewerUserID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

// Following 处理 GET /api/v1/feed/following。
// 这是第八阶段关注流的 HTTP 入口。
func (h *Handler) Following(c *gin.Context) {
	// 关注流和首页一样，也支持 limit 分页参数。
	// 所以这里复用了前面已经抽出来的 parseRequestedLimit。
	requestedLimit, err := parseRequestedLimit(c)
	if err != nil {
		response.Error(c, err)
		return
	}

	// 关注流必须登录，所以这里拿不到当前用户就是鉴权失败。
	viewerUserID, ok := authDomain.CurrentUserIDFromContext(c)
	if !ok {
		response.Error(c, appErrors.ErrUnauthorized)
		return
	}

	// 到这里，HTTP 世界里的工作基本结束。
	// 接下来交给 Service 去做真正的“关注流查询 + 卡片组装 + 分页游标生成”。
	resp, err := h.service.GetFollowing(c.Request.Context(), viewerUserID, c.Query("cursor"), requestedLimit)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, resp)
}

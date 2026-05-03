package interaction

import (
	"time"

	videoDomain "feed-backend/internal/domain/video"
)

type ToggleLikeResponse struct {
	Liked bool `json:"liked"`
}

type ToggleFavoriteResponse struct {
	Favorited bool `json:"favorited"`
}

type ToggleFollowResponse struct {
	Following bool `json:"following"`
}

type CreateCommentRequest struct {
	Content string `json:"content" binding:"required,max=500"`
}

type CommentItem struct {
	ID        uint64                  `json:"id"`
	Content   string                  `json:"content"`
	CreatedAt time.Time               `json:"created_at"`
	User      videoDomain.UserSummary `json:"user"`
}

type CommentPageResponse struct {
	Items      []*CommentItem `json:"items"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

type commentCursor struct {
	Time time.Time `json:"time"`
	ID   uint64    `json:"id"`
}

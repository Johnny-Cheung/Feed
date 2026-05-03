package user

import (
	"time"

	videoDomain "feed-backend/internal/domain/video"
)

const (
	RelationNone             = "none"
	RelationFollowedByAuthor = "followed_by_author"
	RelationFollowingAuthor  = "following_author"
	RelationMutual           = "mutual"
)

type MeResponse struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
}

type UpdateProfileRequest struct {
	Nickname string `json:"nickname" binding:"required,max=32"`
	Bio      string `json:"bio" binding:"max=200"`
}

type UpdatePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required,min=6,max=32"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=32"`
}

type UserProfileResponse struct {
	ID             uint64  `json:"id"`
	Nickname       string  `json:"nickname"`
	AvatarURL      string  `json:"avatar_url"`
	Bio            string  `json:"bio"`
	RelationStatus *string `json:"relation_status"`
}

type UserCard struct {
	ID             uint64  `json:"id"`
	Nickname       string  `json:"nickname"`
	AvatarURL      string  `json:"avatar_url"`
	Bio            string  `json:"bio"`
	RelationStatus *string `json:"relation_status"`
}

type VideoPageResponse struct {
	Items      []*videoDomain.VideoCard `json:"items"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
}

type UserPageResponse struct {
	Items      []*UserCard `json:"items"`
	NextCursor string      `json:"next_cursor"`
	HasMore    bool        `json:"has_more"`
}

type timeCursor struct {
	Time time.Time `json:"time"`
	ID   uint64    `json:"id"`
}

type videoRef struct {
	VideoID    uint64
	CursorTime time.Time
}

type userRef struct {
	UserID     uint64
	CursorTime time.Time
}

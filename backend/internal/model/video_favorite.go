package model

import "time"

// VideoFavorite 对应 video_favorites 表。
// 这张表保存“哪个用户收藏了哪个视频”。
type VideoFavorite struct {
	// ID 是关系表主键。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_video_favorites_video_created,priority:3;index:idx_video_favorites_user_created,priority:3"`

	// UserID 是收藏用户。
	UserID uint64 `gorm:"column:user_id;type:bigint unsigned;not null;uniqueIndex:uk_video_favorites_user_video,priority:1;index:idx_video_favorites_user_created,priority:1"`

	// VideoID 是被收藏的视频。
	VideoID uint64 `gorm:"column:video_id;type:bigint unsigned;not null;uniqueIndex:uk_video_favorites_user_video,priority:2;index:idx_video_favorites_video_created,priority:1"`

	// CreatedAt 是收藏时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null;index:idx_video_favorites_video_created,priority:2;index:idx_video_favorites_user_created,priority:2"`
}

// TableName 显式指定表名。
func (VideoFavorite) TableName() string {
	return "video_favorites"
}

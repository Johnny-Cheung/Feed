package model

import "time"

// VideoLike 对应 video_likes 表。
// 这张表保存“哪个用户点赞了哪个视频”。
type VideoLike struct {
	// ID 是关系表主键。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_video_likes_video_created,priority:3;index:idx_video_likes_user_created,priority:3"`

	// UserID 是点赞用户。
	// 它参与两个索引：
	// 1. 与 video_id 组成唯一索引，防止重复点赞
	// 2. 与 created_at、id 组成列表索引，方便查询“我点赞过的视频”
	UserID uint64 `gorm:"column:user_id;type:bigint unsigned;not null;uniqueIndex:uk_video_likes_user_video,priority:1;index:idx_video_likes_user_created,priority:1"`

	// VideoID 是被点赞的视频。
	// 它也参与两个索引：
	// 1. 与 user_id 组成唯一索引
	// 2. 与 created_at、id 组成列表索引，方便统计某个视频的点赞关系
	VideoID uint64 `gorm:"column:video_id;type:bigint unsigned;not null;uniqueIndex:uk_video_likes_user_video,priority:2;index:idx_video_likes_video_created,priority:1"`

	// CreatedAt 是点赞时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null;index:idx_video_likes_video_created,priority:2;index:idx_video_likes_user_created,priority:2"`
}

// TableName 显式指定表名。
func (VideoLike) TableName() string {
	return "video_likes"
}

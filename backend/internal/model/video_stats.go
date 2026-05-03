package model

import "time"

// VideoStats 对应 video_stats 表。
// 这张表专门保存视频的冗余统计数据，避免每次都去点赞表、评论表实时聚合。
type VideoStats struct {
	// VideoID 直接作为主键使用，与 videos.id 一一对应。
	// 这里不自增，因为它本质上就是“哪个视频的统计”。
	VideoID uint64 `gorm:"column:video_id;primaryKey;autoIncrement:false;type:bigint unsigned;index:idx_video_stats_hot_score,priority:2,sort:desc"`

	// LikeCount 是点赞数。
	LikeCount uint32 `gorm:"column:like_count;type:int unsigned;not null;default:0"`

	// CommentCount 是评论数。
	CommentCount uint32 `gorm:"column:comment_count;type:int unsigned;not null;default:0"`

	// FavoriteCount 是收藏数。
	FavoriteCount uint32 `gorm:"column:favorite_count;type:int unsigned;not null;default:0"`

	// HotScore 是首页热度分。
	// 这里建立 hot_score + video_id 复合索引，方便热榜排序。
	HotScore float64 `gorm:"column:hot_score;type:decimal(12,4);not null;default:0;index:idx_video_stats_hot_score,priority:1,sort:desc"`

	// CreatedAt 是创建时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null"`

	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `gorm:"type:datetime(3);not null"`
}

// TableName 显式指定表名。
func (VideoStats) TableName() string {
	return "video_stats"
}

package model

import (
	"time"

	"gorm.io/gorm"
)

// Video 对应 videos 表。
// 这张表保存视频主数据：作者、标题、文件路径、状态、发布时间等。
type Video struct {
	// ID 是视频主键。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_videos_author_published,priority:3;index:idx_videos_status_published,priority:3"`

	// AuthorID 是作者用户 ID。
	// 它参与 author_id + published_at + id 复合索引，
	// 后面查询“某个作者的作品列表”时会用到。
	AuthorID uint64 `gorm:"column:author_id;type:bigint unsigned;not null;index:idx_videos_author_published,priority:1"`

	// Title 是视频标题。
	Title string `gorm:"type:varchar(100);not null"`

	// VideoPath 保存视频相对路径。
	VideoPath string `gorm:"column:video_path;type:varchar(255);not null"`

	// CoverPath 保存封面相对路径。
	CoverPath string `gorm:"column:cover_path;type:varchar(255);not null"`

	// VideoSizeBytes 保存原始视频大小，单位是字节。
	VideoSizeBytes uint64 `gorm:"column:video_size_bytes;type:bigint unsigned;not null"`

	// Status 表示视频状态。
	// 同时参与 status + published_at + id 复合索引，方便公共列表查询。
	Status VideoStatus `gorm:"type:tinyint unsigned;not null;default:1;index:idx_videos_status_published,priority:1"`

	// PublishedAt 表示发布时间。
	// 这个字段是首页和作者作品流的重要排序字段。
	PublishedAt time.Time `gorm:"column:published_at;type:datetime(3);not null;index:idx_videos_author_published,priority:2;index:idx_videos_status_published,priority:2"`

	// CreatedAt 是创建时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null"`

	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `gorm:"type:datetime(3);not null"`

	// DeletedAt 是 Gorm 自带的软删除字段。
	// 只要这个字段不为空，Gorm 默认查询就会自动过滤掉这条记录。
	DeletedAt gorm.DeletedAt `gorm:"type:datetime(3);index:idx_videos_deleted_at"`
}

// TableName 显式指定表名。
func (Video) TableName() string {
	return "videos"
}

package model

import (
	"time"

	"gorm.io/gorm"
)

// Comment 对应 comments 表。
// V1 只做一级评论，所以这里不需要 parent_id。
type Comment struct {
	// ID 是评论主键。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_comments_video_created,priority:3;index:idx_comments_user_created,priority:3"`

	// VideoID 表示这条评论属于哪个视频。
	VideoID uint64 `gorm:"column:video_id;type:bigint unsigned;not null;index:idx_comments_video_created,priority:1"`

	// UserID 表示评论作者。
	UserID uint64 `gorm:"column:user_id;type:bigint unsigned;not null;index:idx_comments_user_created,priority:1"`

	// Content 是评论内容。
	Content string `gorm:"type:varchar(500);not null"`

	// Status 表示评论状态。
	Status CommentStatus `gorm:"type:tinyint unsigned;not null;default:1;index:idx_comments_status_deleted,priority:1"`

	// CreatedAt 是创建时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null;index:idx_comments_video_created,priority:2;index:idx_comments_user_created,priority:2"`

	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `gorm:"type:datetime(3);not null"`

	// DeletedAt 是软删除时间。
	// 和 status 一起组成复合索引，方便过滤正常评论。
	DeletedAt gorm.DeletedAt `gorm:"type:datetime(3);index:idx_comments_status_deleted,priority:2"`
}

// TableName 显式指定表名。
func (Comment) TableName() string {
	return "comments"
}

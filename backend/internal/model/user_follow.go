package model

import "time"

// UserFollow 对应 user_follows 表。
// 它保存“用户 A 关注了用户 B”的关系。
type UserFollow struct {
	// ID 是关系表主键。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_user_follows_user_created,priority:3;index:idx_user_follows_target_created,priority:3"`

	// UserID 是关注动作的发起人，也就是“我关注了谁”里的“我”。
	UserID uint64 `gorm:"column:user_id;type:bigint unsigned;not null;uniqueIndex:uk_user_follows_user_target,priority:1;index:idx_user_follows_user_created,priority:1"`

	// FollowUserID 是被关注的人。
	FollowUserID uint64 `gorm:"column:follow_user_id;type:bigint unsigned;not null;uniqueIndex:uk_user_follows_user_target,priority:2;index:idx_user_follows_target_created,priority:1"`

	// CreatedAt 是关注时间。
	CreatedAt time.Time `gorm:"type:datetime(3);not null;index:idx_user_follows_user_created,priority:2;index:idx_user_follows_target_created,priority:2"`
}

// TableName 显式指定表名。
func (UserFollow) TableName() string {
	return "user_follows"
}

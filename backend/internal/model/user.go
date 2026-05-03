package model

import "time"

// User 对应 users 表。
// 这张表既保存账号信息，也保存基础资料信息。
// 当前阶段先不再额外拆 user_profiles，方便初学者理解和落地。
type User struct {
	// ID 是用户主键。
	// 使用 bigint unsigned，是为了给后续数据增长留足空间。
	ID uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned;index:idx_users_status_created,priority:3"`

	// Username 是登录用户名，要求全局唯一。
	// 这里通过 uniqueIndex 建唯一索引，数据库层会保证不能重复。
	Username string `gorm:"type:varchar(32);not null;uniqueIndex:uk_users_username"`

	// PasswordHash 保存经过 bcrypt 处理后的密码哈希值。
	// 这里绝对不能存明文密码。
	PasswordHash string `gorm:"column:password_hash;type:varchar(255);not null"`

	// Nickname 是展示给前端看的昵称。
	// 它允许重复，所以只建普通索引，不建唯一索引。
	Nickname string `gorm:"type:varchar(32);not null;index"`

	// AvatarPath 保存头像的相对路径，例如 avatars/2026/04/21/xxx.png。
	AvatarPath string `gorm:"column:avatar_path;type:varchar(255)"`

	// Bio 是个人简介。
	Bio string `gorm:"type:varchar(200)"`

	// Status 表示用户状态。
	// 这里同时把它放进 status+created_at+id 复合索引，方便后续按状态过滤用户列表。
	Status UserStatus `gorm:"type:tinyint unsigned;not null;default:1;index:idx_users_status_created,priority:1"`

	// CreatedAt 是创建时间。
	// precision:3 对应 datetime(3)，也就是毫秒精度。
	CreatedAt time.Time `gorm:"type:datetime(3);not null;index:idx_users_status_created,priority:2"`

	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `gorm:"type:datetime(3);not null"`
}

// TableName 显式指定表名，避免以后结构体重命名时影响表名映射。
func (User) TableName() string {
	return "users"
}

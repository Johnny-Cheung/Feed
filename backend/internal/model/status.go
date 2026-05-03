package model

// 下面这些自定义类型，本质上还是整数。
// 之所以单独定义，是为了让代码语义更清晰：
// 看到 UserStatus，你就知道它表示“用户状态”；
// 看到 VideoStatus，你就知道它表示“视频状态”。

// UserStatus 表示用户状态。
type UserStatus uint8

const (
	// UserStatusActive 表示用户处于正常可用状态。
	UserStatusActive UserStatus = 1

	// UserStatusDisabled 表示用户被禁用。
	// V1 当前还没做后台禁用功能，但先把状态位留出来。
	UserStatusDisabled UserStatus = 2
)

// VideoStatus 表示视频状态。
type VideoStatus uint8

const (
	// VideoStatusPublished 表示视频已发布，可被正常读取。
	VideoStatusPublished VideoStatus = 1

	// VideoStatusHidden 表示视频被隐藏。
	// V1 当前先不做隐藏后台，但结构先预留。
	VideoStatusHidden VideoStatus = 2
)

// CommentStatus 表示评论状态。
type CommentStatus uint8

const (
	// CommentStatusNormal 表示评论正常可见。
	CommentStatusNormal CommentStatus = 1

	// CommentStatusDeleted 表示评论已被逻辑删除。
	CommentStatusDeleted CommentStatus = 2
)

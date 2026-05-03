package video

import (
	"mime/multipart"
	"time"
)

// PublishRequest 是“发布视频”接口需要的输入。
// 由于请求类型是 multipart/form-data，所以这里直接持有文件头。
// 教学理解：
// - Handler 层先从 Gin Context 中把表单字段和文件取出来
// - 再组装成 PublishRequest 传给 Service
// - 这样 Service 就不用关心 Gin 的 API，只关心“业务上需要哪些输入”
type PublishRequest struct {
	Title       string
	VideoHeader *multipart.FileHeader
	CoverHeader *multipart.FileHeader
}

// UpdateRequest 是“修改视频标题/封面”接口需要的输入。
// Title 用指针是为了区分：
// - 没传 title
// - 传了 title 但内容为空
// 这是一种常见技巧：
// 字符串零值是 ""，如果不用指针，就分不清“客户端没传”和“客户端传了空字符串”。
type UpdateRequest struct {
	Title       *string
	CoverHeader *multipart.FileHeader
}

// UserSummary 对应规划文档中的作者摘要对象。
type UserSummary struct {
	ID        uint64 `json:"id"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
}

// VideoStatsObject 对应规划文档中的统计对象。
type VideoStatsObject struct {
	LikeCount     uint32 `json:"like_count"`
	CommentCount  uint32 `json:"comment_count"`
	FavoriteCount uint32 `json:"favorite_count"`
}

// ViewerStateObject 对应规划文档中的“当前查看者状态”对象。
// 阶段六虽然还没有点赞/收藏/关注接口，但详情接口可以先把结构留好。
type ViewerStateObject struct {
	Liked           bool `json:"liked"`
	Favorited       bool `json:"favorited"`
	FollowingAuthor bool `json:"following_author"`
}

// VideoCard 对应规划文档里的视频卡片响应结构。
// 它也是当前第六阶段最核心的“对外输出对象”：
// 发布成功、查看详情、更新视频后，最终都返回它。
type VideoCard struct {
	ID          uint64            `json:"id"`
	Title       string            `json:"title"`
	VideoURL    string            `json:"video_url"`
	CoverURL    string            `json:"cover_url"`
	PublishedAt time.Time         `json:"published_at"`
	Author      UserSummary       `json:"author"`
	Stats       VideoStatsObject  `json:"stats"`
	ViewerState ViewerStateObject `json:"viewer_state"`
}

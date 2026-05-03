package feed

import (
	"time"

	videoDomain "feed-backend/internal/domain/video"
)

// FeedPageResponse 是 Feed 模块统一分页响应。
// 当前第七阶段首页热榜流和第八阶段关注流都会返回这个结构。
type FeedPageResponse struct {
	Items      []*videoDomain.VideoCard `json:"items"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
}

// HomeFeedResponse 是首页热榜接口的分页响应别名。
// 保留这个名字，是为了让“首页”这条链路的阅读语义更直观。
type HomeFeedResponse = FeedPageResponse

// FollowingFeedResponse 是关注流接口的分页响应别名。
type FollowingFeedResponse = FeedPageResponse

// hotCursor 是首页热榜使用的分页游标。
// 排序规则是 hot_score DESC, id DESC，所以游标也必须同时记住 score 和 id。
// 为什么不能只记 video_id？
// 因为首页不是单纯按 ID 排序，而是先按热度，再按 ID。
// 只存一个 ID，下一页就无法准确知道应该从哪个热度位置继续翻。
type hotCursor struct {
	Score float64 `json:"score"`
	ID    uint64  `json:"id"`
}

// timeCursor 是时间流使用的分页游标。
// 第八阶段关注流的排序规则是 published_at DESC, id DESC，
// 所以游标需要同时记住时间和 ID。
// 这里的思路和首页热榜 cursor 完全一样：
// - 首页是按 score + id 排
// - 关注流是按 time + id 排
// 只要排序键不止一个字段，cursor 就必须把这些字段都记住。
type timeCursor struct {
	Time time.Time `json:"time"`
	ID   uint64    `json:"id"`
}

// HotFeedRef 表示“热榜里的一条候选记录”。
// 这里只放组装分页和排序所需的最小信息：视频 ID 和热度分。
// 它可以看成是“首页读取过程中的中间结构体”：
// - Redis 热榜里本来就只有 video_id + score
// - MySQL 回源时我们也先查出这两个字段
// - 等到后面真正组装卡片时，再补完整的视频、作者和统计信息
type HotFeedRef struct {
	VideoID  uint64
	HotScore float64
}

// FollowingFeedRef 表示“关注流里的一条候选记录”。
// 它和首页热榜不同，不关心热度分，只关心发布时间和视频 ID。
// 你可以把它看成“时间流版本的 HotFeedRef”：
// - 首页热榜候选：video_id + hot_score
// - 关注流候选：video_id + published_at
type FollowingFeedRef struct {
	VideoID     uint64
	PublishedAt time.Time
}

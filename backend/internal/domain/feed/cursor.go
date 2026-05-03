package feed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeHotCursor 把 query 参数里的 cursor 解析成首页热榜游标。
// 空字符串表示第一页。
func decodeHotCursor(raw string) (*hotCursor, error) {
	// 先做最基本的空白清理。
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// 当前项目约定 cursor 统一使用 base64(json)。
	// 所以先 base64 解码，再 JSON 反序列化。
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode hot cursor: %w", err)
	}

	var cursor hotCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("unmarshal hot cursor: %w", err)
	}

	// ID 是最基础的兜底字段，缺了它就说明 cursor 结构不完整。
	if cursor.ID == 0 {
		return nil, fmt.Errorf("hot cursor id is invalid")
	}

	return &cursor, nil
}

// encodeHotCursor 把首页热榜游标编码成可放进 URL 的字符串。
func encodeHotCursor(cursor hotCursor) (string, error) {
	// 先变成 JSON，再编码成 URL 友好的 base64 字符串。
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal hot cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// isAfterHotCursor 判断一条候选记录是否处于“当前 cursor 之后”。
// 因为首页排序是倒序，所以“之后”意味着：
// - 热度分更小
// - 或者热度分相同但视频 ID 更小
func isAfterHotCursor(ref HotFeedRef, cursor *hotCursor) bool {
	// cursor 为空表示第一页，这时任何候选都算合法。
	if cursor == nil {
		return true
	}

	// 这段判断完全对应首页的排序规则：
	// hot_score DESC, id DESC
	// 所以“下一页”就是：
	// score 更小，或者 score 相同但 id 更小。
	return ref.HotScore < cursor.Score || (ref.HotScore == cursor.Score && ref.VideoID < cursor.ID)
}

// decodeTimeCursor 把 query 参数里的 cursor 解析成时间流游标。
// 空字符串表示第一页。
func decodeTimeCursor(raw string) (*timeCursor, error) {
	// 时间流 cursor 的编码方式和首页保持一致，仍然是 base64(json)。
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode time cursor: %w", err)
	}

	var cursor timeCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("unmarshal time cursor: %w", err)
	}

	if cursor.ID == 0 {
		return nil, fmt.Errorf("time cursor id is invalid")
	}
	if cursor.Time.IsZero() {
		return nil, fmt.Errorf("time cursor time is invalid")
	}

	// 关注流排序依赖时间比较。
	// 统一转成 UTC，能减少不同时间字符串格式带来的混乱。
	// 统一转成 UTC，避免不同时区字符串引起比较混乱。
	cursor.Time = cursor.Time.UTC()
	return &cursor, nil
}

// encodeTimeCursor 把时间流游标编码成可放进 URL 的字符串。
func encodeTimeCursor(cursor timeCursor) (string, error) {
	// Round(0) 的目的是去掉单调时钟信息，保证 JSON 编码结果稳定。
	cursor.Time = cursor.Time.UTC().Round(0)

	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal time cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// isAfterTimeCursor 判断一条候选记录是否位于时间流当前页之后。
// 时间流排序规则是 published_at DESC, id DESC，所以“之后”意味着：
// - 发布时间更早
// - 或者发布时间相同但视频 ID 更小
func isAfterTimeCursor(ref FollowingFeedRef, cursor *timeCursor) bool {
	// 第八阶段虽然当前查询直接在 SQL 里带了 cursor 条件，
	// 这个函数仍然把“时间流分页规则”单独表达出来，方便后续复用或对照理解。
	if cursor == nil {
		return true
	}

	refTime := ref.PublishedAt.UTC()
	cursorTime := cursor.Time.UTC()
	return refTime.Before(cursorTime) || (refTime.Equal(cursorTime) && ref.VideoID < cursor.ID)
}

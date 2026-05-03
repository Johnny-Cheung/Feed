package feedcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
)

const (
	homeHotFeedKey                = "feed:home:hot"
	videoTopCommentsKeyPrefix     = "feed:video:comments:top50:v2:"
	DefaultHotCommentCacheEntries = 50
	DefaultHotCommentCacheTTL     = time.Hour
)

type CommentBrief struct {
	ID        uint64     `json:"id"`
	VideoID   uint64     `json:"video_id"`
	UserID    uint64     `json:"user_id"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	User      *UserBrief `json:"user,omitempty"`
}

func NewCommentBrief(comment *model.Comment) *CommentBrief {
	if comment == nil {
		return nil
	}

	return &CommentBrief{
		ID:        comment.ID,
		VideoID:   comment.VideoID,
		UserID:    comment.UserID,
		Content:   comment.Content,
		CreatedAt: comment.CreatedAt.UTC().Round(0),
	}
}

func (c *Cache) IsHomeHotVideo(ctx context.Context, videoID uint64) (bool, error) {
	if c == nil || c.redis == nil || videoID == 0 {
		return false, nil
	}

	_, err := c.redis.ZScore(ctx, homeHotFeedKey, strconv.FormatUint(videoID, 10)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check home hot video: %w", err)
	}
	return true, nil
}

func (c *Cache) LoadTopComments(ctx context.Context, videoID uint64) ([]CommentBrief, bool, error) {
	if c == nil || c.redis == nil || videoID == 0 {
		return nil, false, nil
	}

	payload, err := c.redis.Get(ctx, videoTopCommentsKey(videoID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load top comments cache: %w", err)
	}

	var comments []CommentBrief
	if err := json.Unmarshal(payload, &comments); err != nil {
		return nil, false, fmt.Errorf("unmarshal top comments cache: %w", err)
	}

	normalized := normalizeCommentBriefs(videoID, comments, 0)
	if len(normalized) != len(comments) {
		return nil, false, fmt.Errorf("top comments cache is invalid")
	}
	return normalized, true, nil
}

func (c *Cache) StoreTopComments(ctx context.Context, videoID uint64, comments []CommentBrief, maxEntries int, ttl time.Duration) error {
	if c == nil || c.redis == nil || videoID == 0 || ttl <= 0 {
		return nil
	}

	comments = normalizeCommentBriefs(videoID, comments, maxEntries)
	if comments == nil {
		comments = []CommentBrief{}
	}
	payload, err := json.Marshal(comments)
	if err != nil {
		return fmt.Errorf("marshal top comments cache: %w", err)
	}

	if err := c.redis.Set(ctx, videoTopCommentsKey(videoID), payload, ttl).Err(); err != nil {
		return fmt.Errorf("store top comments cache: %w", err)
	}
	return nil
}

func (c *Cache) DeleteTopComments(ctx context.Context, videoIDs ...uint64) error {
	if c == nil || c.redis == nil || len(videoIDs) == 0 {
		return nil
	}

	keys := make([]string, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		keys = append(keys, videoTopCommentsKey(videoID))
	}
	if len(keys) == 0 {
		return nil
	}

	if err := c.redis.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("delete top comments cache: %w", err)
	}
	return nil
}

func videoTopCommentsKey(videoID uint64) string {
	return videoTopCommentsKeyPrefix + strconv.FormatUint(videoID, 10)
}

func normalizeCommentBriefs(videoID uint64, comments []CommentBrief, maxEntries int) []CommentBrief {
	if len(comments) == 0 {
		return nil
	}

	result := make([]CommentBrief, 0, len(comments))
	seen := make(map[uint64]struct{}, len(comments))
	for _, comment := range comments {
		if comment.ID == 0 || comment.VideoID != videoID || comment.UserID == 0 || comment.CreatedAt.IsZero() {
			continue
		}
		if _, exists := seen[comment.ID]; exists {
			continue
		}
		seen[comment.ID] = struct{}{}
		comment.CreatedAt = comment.CreatedAt.UTC().Round(0)
		if comment.User != nil && comment.User.UserID != comment.UserID {
			comment.User = nil
		}
		result = append(result, comment)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	if maxEntries > 0 && len(result) > maxEntries {
		result = result[:maxEntries]
	}
	return result
}

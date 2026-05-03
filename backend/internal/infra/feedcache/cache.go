package feedcache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	videoBaseKeyPrefix             = "feed:video:base:v1:"
	userBriefKeyPrefix             = "feed:user:brief:v1:"
	legacyVideoStatsKeyPrefix      = "feed:video:stats:v1:"
	videoStatsKeyPrefix            = "feed:video:stats:v2:"
	followingInboxKeyPrefix        = "feed:user:following:inbox:v1:"
	userActiveKeyPrefix            = "feed:user:active:v1:"
	followedPullAuthorsKeyPrefix   = "feed:user:followed_pull_authors:v1:"
	authorOutboxKeyPrefix          = "feed:author:outbox:v1:"
	authorFollowingModeKeyPrefix   = "feed:author:following_mode:v1:"
	userLikesRecentKeyPrefix       = "feed:user:likes:recent5d:v1:"
	userLikesOnDemandKeyPrefix     = "feed:user:likes:ondemand:v1:"
	userFavoritesRecentKeyPrefix   = "feed:user:favorites:recent5d:v1:"
	userFavoritesOnDemandKeyPrefix = "feed:user:favorites:ondemand:v1:"
	userFollowsFullKeyPrefix       = "feed:user:follows:full:v1:"
	userRelationMetaKeyPrefix      = "feed:user:rel:meta:v1:"
	videoRelationStreamKey         = "feed:stream:video_relation:v1"
	homeHotDirtyKey                = "feed:home:hot:dirty:v1"
	scanCount                      = 200
)

const (
	videoStatsVideoIDField       = "video_id"
	videoStatsLikeCountField     = "like_count"
	videoStatsCommentCountField  = "comment_count"
	videoStatsFavoriteCountField = "favorite_count"
	videoStatsHotScoreField      = "hot_score"
)

const (
	FollowingInboxMaxEntries = 1000
	FollowingActiveTTL       = 7 * 24 * time.Hour
)

const (
	VideoRelationStreamDefaultMaxLen = 1000000
	VideoRelationTypeLike            = "like"
	VideoRelationTypeFavorite        = "favorite"
)

type Cache struct {
	db    *gorm.DB
	redis *redis.Client
}

type VideoRelationStreamEvent struct {
	EventID      string
	EventType    string
	RelationType string
	UserID       uint64
	VideoID      uint64
	Active       bool
	OccurredAt   time.Time
	MaxLen       int64
}

type VideoBase struct {
	VideoID     uint64    `json:"video_id"`
	Title       string    `json:"title"`
	VideoPath   string    `json:"video_path"`
	CoverPath   string    `json:"cover_path"`
	PublishedAt time.Time `json:"published_at"`
	AuthorID    uint64    `json:"author_id"`
}

type UserBrief struct {
	UserID     uint64 `json:"user_id"`
	Nickname   string `json:"nickname"`
	AvatarPath string `json:"avatar_path"`
}

type VideoStats struct {
	VideoID       uint64  `json:"video_id"`
	LikeCount     uint32  `json:"like_count"`
	CommentCount  uint32  `json:"comment_count"`
	FavoriteCount uint32  `json:"favorite_count"`
	HotScore      float64 `json:"hot_score"`
}

func NewCache(db *gorm.DB, redisClient *redis.Client) *Cache {
	return &Cache{
		db:    db,
		redis: redisClient,
	}
}

func NewVideoBase(video *model.Video) *VideoBase {
	if video == nil {
		return nil
	}

	return &VideoBase{
		VideoID:     video.ID,
		Title:       video.Title,
		VideoPath:   video.VideoPath,
		CoverPath:   video.CoverPath,
		PublishedAt: video.PublishedAt.UTC().Round(0),
		AuthorID:    video.AuthorID,
	}
}

func NewUserBrief(user *model.User) *UserBrief {
	if user == nil {
		return nil
	}

	return &UserBrief{
		UserID:     user.ID,
		Nickname:   user.Nickname,
		AvatarPath: user.AvatarPath,
	}
}

func NewVideoStats(stats *model.VideoStats) *VideoStats {
	if stats == nil {
		return nil
	}

	return &VideoStats{
		VideoID:       stats.VideoID,
		LikeCount:     stats.LikeCount,
		CommentCount:  stats.CommentCount,
		FavoriteCount: stats.FavoriteCount,
		HotScore:      stats.HotScore,
	}
}

func (c *Cache) loadJSONMapByUint64Keys(ctx context.Context, keys []string, ids []uint64, decode func([]byte) (uint64, any, error)) (map[uint64]any, []uint64, error) {
	result := make(map[uint64]any, len(ids))
	if len(ids) == 0 {
		return result, nil, nil
	}
	if c == nil || c.redis == nil {
		return result, append([]uint64(nil), ids...), nil
	}

	values, err := c.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, nil, err
	}

	missing := make([]uint64, 0, len(ids))
	for i, value := range values {
		id := ids[i]
		if value == nil {
			missing = append(missing, id)
			continue
		}

		raw, ok := redisValueToString(value)
		if !ok {
			missing = append(missing, id)
			continue
		}

		decodedID, decoded, decodeErr := decode([]byte(raw))
		if decodeErr != nil || decodedID != id || decodedID == 0 {
			missing = append(missing, id)
			continue
		}

		result[id] = decoded
	}

	return result, missing, nil
}

func (c *Cache) storeJSONMap(ctx context.Context, keys []string, payloads [][]byte) error {
	if len(keys) == 0 || c == nil || c.redis == nil {
		return nil
	}

	values := make([]interface{}, 0, len(keys)*2)
	for i := range keys {
		values = append(values, keys[i], payloads[i])
	}

	if err := c.redis.MSet(ctx, values...).Err(); err != nil {
		return err
	}
	return nil
}

func videoBaseKey(videoID uint64) string {
	return videoBaseKeyPrefix + strconv.FormatUint(videoID, 10)
}

func userBriefKey(userID uint64) string {
	return userBriefKeyPrefix + strconv.FormatUint(userID, 10)
}

func videoStatsKey(videoID uint64) string {
	return videoStatsKeyPrefix + strconv.FormatUint(videoID, 10)
}

func VideoRelationStreamKey() string {
	return videoRelationStreamKey
}

func HomeHotDirtyKey() string {
	return homeHotDirtyKey
}

func legacyVideoStatsKey(videoID uint64) string {
	return legacyVideoStatsKeyPrefix + strconv.FormatUint(videoID, 10)
}

func followingInboxKey(userID uint64) string {
	return followingInboxKeyPrefix + strconv.FormatUint(userID, 10)
}

func userActiveKey(userID uint64) string {
	return userActiveKeyPrefix + strconv.FormatUint(userID, 10)
}

func followedPullAuthorsKey(userID uint64) string {
	return followedPullAuthorsKeyPrefix + strconv.FormatUint(userID, 10)
}

func authorOutboxKey(authorID uint64) string {
	return authorOutboxKeyPrefix + strconv.FormatUint(authorID, 10)
}

func authorFollowingModeKey(authorID uint64) string {
	return authorFollowingModeKeyPrefix + strconv.FormatUint(authorID, 10)
}

func userLikesRecentKey(userID uint64) string {
	return userLikesRecentKeyPrefix + strconv.FormatUint(userID, 10)
}

func userLikesOnDemandKey(userID uint64) string {
	return userLikesOnDemandKeyPrefix + strconv.FormatUint(userID, 10)
}

func userFavoritesRecentKey(userID uint64) string {
	return userFavoritesRecentKeyPrefix + strconv.FormatUint(userID, 10)
}

func userFavoritesOnDemandKey(userID uint64) string {
	return userFavoritesOnDemandKeyPrefix + strconv.FormatUint(userID, 10)
}

func userFollowsFullKey(userID uint64) string {
	return userFollowsFullKeyPrefix + strconv.FormatUint(userID, 10)
}

func userRelationMetaKey(userID uint64) string {
	return userRelationMetaKeyPrefix + strconv.FormatUint(userID, 10)
}

func addUserRelationTTLToPipeline(ctx context.Context, pipe redis.Pipeliner, userID uint64) {
	if userID == 0 {
		return
	}

	for _, key := range []string{
		userFollowsFullKey(userID),
		userLikesRecentKey(userID),
		userLikesOnDemandKey(userID),
		userFavoritesRecentKey(userID),
		userFavoritesOnDemandKey(userID),
		userRelationMetaKey(userID),
		followedPullAuthorsKey(userID),
	} {
		pipe.Expire(ctx, key, FollowingActiveTTL)
	}
}

func keysForVideoBaseIDs(videoIDs []uint64) []string {
	keys := make([]string, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		keys = append(keys, videoBaseKey(videoID))
	}
	return keys
}

func keysForUserIDs(userIDs []uint64) []string {
	keys := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		keys = append(keys, userBriefKey(userID))
	}
	return keys
}

func keysForVideoStatsIDs(videoIDs []uint64) []string {
	keys := make([]string, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		keys = append(keys, videoStatsKey(videoID))
	}
	return keys
}

func legacyKeysForVideoStatsIDs(videoIDs []uint64) []string {
	keys := make([]string, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		keys = append(keys, legacyVideoStatsKey(videoID))
	}
	return keys
}

func redisValueToString(value interface{}) (string, bool) {
	switch current := value.(type) {
	case string:
		return current, true
	case []byte:
		return string(current), true
	case int:
		return strconv.Itoa(current), true
	case int64:
		return strconv.FormatInt(current, 10), true
	case uint64:
		return strconv.FormatUint(current, 10), true
	default:
		return "", false
	}
}

func idsToFields(ids []uint64) []string {
	fields := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		fields = append(fields, strconv.FormatUint(id, 10))
	}
	return fields
}

func idsMissingFromMap[T any](ids []uint64, loaded map[uint64]T) []uint64 {
	missing := make([]uint64, 0)
	for _, id := range ids {
		if _, ok := loaded[id]; ok {
			continue
		}
		missing = append(missing, id)
	}
	return missing
}

func decodeVideoBase(payload []byte) (uint64, any, error) {
	var base VideoBase
	if err := json.Unmarshal(payload, &base); err != nil {
		return 0, nil, fmt.Errorf("unmarshal video base: %w", err)
	}
	base.PublishedAt = base.PublishedAt.UTC().Round(0)
	return base.VideoID, &base, nil
}

func decodeUserBrief(payload []byte) (uint64, any, error) {
	var brief UserBrief
	if err := json.Unmarshal(payload, &brief); err != nil {
		return 0, nil, fmt.Errorf("unmarshal user brief: %w", err)
	}
	return brief.UserID, &brief, nil
}

func decodeVideoStats(payload []byte) (uint64, any, error) {
	var stats VideoStats
	if err := json.Unmarshal(payload, &stats); err != nil {
		return 0, nil, fmt.Errorf("unmarshal video stats: %w", err)
	}
	return stats.VideoID, &stats, nil
}

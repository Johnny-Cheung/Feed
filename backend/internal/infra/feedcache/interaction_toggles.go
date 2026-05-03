package feedcache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var toggleVideoRelationAndStatsScript = redis.NewScript(`
local relation_key = KEYS[1]
local stats_key = KEYS[2]
local stream_key = KEYS[3]

local relation_field = ARGV[1]
local active = ARGV[2]
local ttl_seconds = tonumber(ARGV[3])
local video_id = ARGV[4]
local init_like_count = ARGV[5]
local init_comment_count = ARGV[6]
local init_favorite_count = ARGV[7]
local init_hot_score = ARGV[8]
local counter_field = ARGV[9]
local stream_max_len = tonumber(ARGV[10])
local event_id = ARGV[11]
local event_type = ARGV[12]
local relation_type = ARGV[13]
local user_id = ARGV[14]
local occurred_at = ARGV[15]

if redis.call("EXISTS", stats_key) == 0 then
	redis.call(
		"HSET",
		stats_key,
		"video_id", video_id,
		"like_count", init_like_count,
		"comment_count", init_comment_count,
		"favorite_count", init_favorite_count,
		"hot_score", init_hot_score
	)
end

local current = redis.call("HGET", relation_key, relation_field)
local changed = 0
local delta = 0

if active == "1" then
	if current ~= "1" then
		changed = 1
		delta = 1
		redis.call("HSET", relation_key, relation_field, "1")
	end
else
	if current == "1" then
		changed = 1
		delta = -1
	end
	redis.call("HSET", relation_key, relation_field, "0")
end

redis.call("EXPIRE", relation_key, ttl_seconds)

local new_counter = tonumber(redis.call("HINCRBY", stats_key, counter_field, delta))
if new_counter < 0 then
	new_counter = 0
	redis.call("HSET", stats_key, counter_field, "0")
end

redis.call("HSET", stats_key, "video_id", video_id)

if changed == 1 and stream_max_len ~= nil and stream_max_len > 0 then
	redis.call(
		"XADD",
		stream_key,
		"MAXLEN",
		"~",
		stream_max_len,
		"*",
		"event_id", event_id,
		"event_type", event_type,
		"relation_type", relation_type,
		"user_id", user_id,
		"video_id", video_id,
		"active", active,
		"occurred_at", occurred_at
	)
end

return {
	changed,
	redis.call("HGET", stats_key, "like_count"),
	redis.call("HGET", stats_key, "comment_count"),
	redis.call("HGET", stats_key, "favorite_count"),
	redis.call("HGET", stats_key, "hot_score")
}
`)

var incrementVideoStatsCounterScript = redis.NewScript(`
local stats_key = KEYS[1]

local video_id = ARGV[1]
local init_like_count = ARGV[2]
local init_comment_count = ARGV[3]
local init_favorite_count = ARGV[4]
local init_hot_score = ARGV[5]
local counter_field = ARGV[6]
local delta = tonumber(ARGV[7])

if redis.call("EXISTS", stats_key) == 0 then
	redis.call(
		"HSET",
		stats_key,
		"video_id", video_id,
		"like_count", init_like_count,
		"comment_count", init_comment_count,
		"favorite_count", init_favorite_count,
		"hot_score", init_hot_score
	)
end

local new_counter = tonumber(redis.call("HINCRBY", stats_key, counter_field, delta))
if new_counter < 0 then
	new_counter = 0
	redis.call("HSET", stats_key, counter_field, "0")
end

redis.call("HSET", stats_key, "video_id", video_id)

return {
	redis.call("HGET", stats_key, "like_count"),
	redis.call("HGET", stats_key, "comment_count"),
	redis.call("HGET", stats_key, "favorite_count"),
	redis.call("HGET", stats_key, "hot_score")
}
`)

func (c *Cache) EnsureLikeRelationCached(ctx context.Context, userID, videoID uint64, publishedAt time.Time) error {
	return c.ensureVideoRelationCached(ctx, userLikesRecentKey(userID), userLikesOnDemandKey(userID), "video_likes", userID, videoID, publishedAt, func(meta *viewerRelationMeta) bool {
		return meta.LikesRecentReady
	})
}

func (c *Cache) EnsureFavoriteRelationCached(ctx context.Context, userID, videoID uint64, publishedAt time.Time) error {
	return c.ensureVideoRelationCached(ctx, userFavoritesRecentKey(userID), userFavoritesOnDemandKey(userID), "video_favorites", userID, videoID, publishedAt, func(meta *viewerRelationMeta) bool {
		return meta.FavoritesRecentReady
	})
}

func (c *Cache) ToggleLikeRelationAndStats(ctx context.Context, userID, videoID uint64, publishedAt time.Time, liked bool, initialStats *VideoStats, streamEvent VideoRelationStreamEvent) (bool, *VideoStats, error) {
	redisKey := userLikesOnDemandKey(userID)
	if isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC())) {
		redisKey = userLikesRecentKey(userID)
	}
	return c.toggleVideoRelationAndStats(ctx, redisKey, videoID, liked, initialStats, videoStatsLikeCountField, streamEvent)
}

func (c *Cache) ToggleFavoriteRelationAndStats(ctx context.Context, userID, videoID uint64, publishedAt time.Time, favorited bool, initialStats *VideoStats, streamEvent VideoRelationStreamEvent) (bool, *VideoStats, error) {
	redisKey := userFavoritesOnDemandKey(userID)
	if isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC())) {
		redisKey = userFavoritesRecentKey(userID)
	}
	return c.toggleVideoRelationAndStats(ctx, redisKey, videoID, favorited, initialStats, videoStatsFavoriteCountField, streamEvent)
}

func (c *Cache) IncrementVideoCommentCount(ctx context.Context, videoID uint64, delta int64, initialStats *VideoStats) (*VideoStats, error) {
	return c.incrementVideoStatsCounter(ctx, videoID, delta, initialStats, videoStatsCommentCountField)
}

func (c *Cache) ensureVideoRelationCached(
	ctx context.Context,
	recentKey string,
	onDemandKey string,
	tableName string,
	userID uint64,
	videoID uint64,
	publishedAt time.Time,
	recentReady func(*viewerRelationMeta) bool,
) error {
	if c == nil || c.redis == nil || userID == 0 || videoID == 0 {
		return nil
	}

	recent := isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC()))
	redisKey := onDemandKey
	if recent {
		redisKey = recentKey
	}

	field := strconv.FormatUint(videoID, 10)
	value, err := c.redis.HGet(ctx, redisKey, field).Result()
	if err == nil {
		if value == "1" || value == "0" {
			return nil
		}
	} else if err != redis.Nil {
		return fmt.Errorf("load cached video relation: %w", err)
	}

	if recent {
		meta, err := c.loadViewerRelationMeta(ctx, userID)
		if err != nil {
			return err
		}
		if recentReady != nil && recentReady(meta) {
			return nil
		}
	}

	loaded, err := c.queryRelationState(ctx, tableName, "video_id", userID, []uint64{videoID})
	if err != nil {
		return err
	}
	seedValue := "0"
	if _, ok := loaded[videoID]; ok {
		seedValue = "1"
	}

	pipe := c.redis.Pipeline()
	pipe.HSetNX(ctx, redisKey, field, seedValue)
	pipe.Expire(ctx, redisKey, FollowingActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store cached video relation: %w", err)
	}
	return nil
}

func (c *Cache) LoadVideoRelationState(ctx context.Context, relationType string, userID, videoID uint64, publishedAt time.Time) (bool, bool, error) {
	if c == nil || c.redis == nil || userID == 0 || videoID == 0 {
		return false, false, nil
	}

	var recentKey string
	var onDemandKey string
	switch relationType {
	case VideoRelationTypeLike:
		recentKey = userLikesRecentKey(userID)
		onDemandKey = userLikesOnDemandKey(userID)
	case VideoRelationTypeFavorite:
		recentKey = userFavoritesRecentKey(userID)
		onDemandKey = userFavoritesOnDemandKey(userID)
	default:
		return false, false, fmt.Errorf("unknown video relation type %q", relationType)
	}

	keys := []string{onDemandKey, recentKey}
	if isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC())) {
		keys = []string{recentKey, onDemandKey}
	}

	field := strconv.FormatUint(videoID, 10)
	for _, key := range keys {
		value, err := c.redis.HGet(ctx, key, field).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return false, false, fmt.Errorf("load video relation state: %w", err)
		}

		switch value {
		case "1":
			return true, true, nil
		case "0":
			return false, true, nil
		}
	}
	return false, false, nil
}

func (c *Cache) toggleVideoRelationAndStats(ctx context.Context, redisKey string, videoID uint64, active bool, initialStats *VideoStats, counterField string, streamEvent VideoRelationStreamEvent) (bool, *VideoStats, error) {
	if c == nil || c.redis == nil || videoID == 0 {
		return false, nil, nil
	}
	if initialStats == nil {
		initialStats = &VideoStats{VideoID: videoID}
	}

	activeValue := "0"
	if active {
		activeValue = "1"
	}
	if streamEvent.MaxLen <= 0 {
		streamEvent.MaxLen = VideoRelationStreamDefaultMaxLen
	}

	values, err := toggleVideoRelationAndStatsScript.Run(
		ctx,
		c.redis,
		[]string{redisKey, videoStatsKey(videoID), videoRelationStreamKey},
		strconv.FormatUint(videoID, 10),
		activeValue,
		strconv.FormatInt(int64(FollowingActiveTTL/time.Second), 10),
		strconv.FormatUint(videoID, 10),
		strconv.FormatUint(uint64(initialStats.LikeCount), 10),
		strconv.FormatUint(uint64(initialStats.CommentCount), 10),
		strconv.FormatUint(uint64(initialStats.FavoriteCount), 10),
		strconv.FormatFloat(initialStats.HotScore, 'f', -1, 64),
		counterField,
		strconv.FormatInt(streamEvent.MaxLen, 10),
		streamEvent.EventID,
		streamEvent.EventType,
		streamEvent.RelationType,
		strconv.FormatUint(streamEvent.UserID, 10),
		streamEvent.OccurredAt.UTC().Format(time.RFC3339Nano),
	).Slice()
	if err != nil {
		return false, nil, fmt.Errorf("toggle video relation and stats: %w", err)
	}
	if len(values) < 5 {
		return false, nil, fmt.Errorf("toggle video relation and stats: invalid redis result")
	}

	changed, _ := redisUint64Value(values[0])
	likeCount, _ := redisUint32Value(values[1])
	commentCount, _ := redisUint32Value(values[2])
	favoriteCount, _ := redisUint32Value(values[3])
	hotScore, _ := redisFloat64Value(values[4])

	return changed == 1, &VideoStats{
		VideoID:       videoID,
		LikeCount:     likeCount,
		CommentCount:  commentCount,
		FavoriteCount: favoriteCount,
		HotScore:      hotScore,
	}, nil
}

func (c *Cache) incrementVideoStatsCounter(ctx context.Context, videoID uint64, delta int64, initialStats *VideoStats, counterField string) (*VideoStats, error) {
	if c == nil || c.redis == nil || videoID == 0 || delta == 0 {
		return initialStats, nil
	}
	if initialStats == nil {
		initialStats = &VideoStats{VideoID: videoID}
	}

	values, err := incrementVideoStatsCounterScript.Run(
		ctx,
		c.redis,
		[]string{videoStatsKey(videoID)},
		strconv.FormatUint(videoID, 10),
		strconv.FormatUint(uint64(initialStats.LikeCount), 10),
		strconv.FormatUint(uint64(initialStats.CommentCount), 10),
		strconv.FormatUint(uint64(initialStats.FavoriteCount), 10),
		strconv.FormatFloat(initialStats.HotScore, 'f', -1, 64),
		counterField,
		strconv.FormatInt(delta, 10),
	).Slice()
	if err != nil {
		return nil, fmt.Errorf("increment video stats counter: %w", err)
	}
	if len(values) < 4 {
		return nil, fmt.Errorf("increment video stats counter: invalid redis result")
	}

	likeCount, _ := redisUint32Value(values[0])
	commentCount, _ := redisUint32Value(values[1])
	favoriteCount, _ := redisUint32Value(values[2])
	hotScore, _ := redisFloat64Value(values[3])

	return &VideoStats{
		VideoID:       videoID,
		LikeCount:     likeCount,
		CommentCount:  commentCount,
		FavoriteCount: favoriteCount,
		HotScore:      hotScore,
	}, nil
}

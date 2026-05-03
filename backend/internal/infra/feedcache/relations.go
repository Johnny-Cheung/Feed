package feedcache

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
)

const (
	relationMetaFollowsFullReadyField     = "follows_full_ready_at"
	relationMetaLikesRecentReadyField     = "likes_recent5d_ready_at"
	relationMetaFavoritesRecentReadyField = "favorites_recent5d_ready_at"
	viewerRelationRecentWindow            = 5 * 24 * time.Hour
	viewerRelationWarmupTimeout           = 30 * time.Second
)

type ViewerRelationVideo struct {
	VideoID     uint64
	PublishedAt time.Time
}

type ViewerRelations struct {
	LikedVideoIDs     map[uint64]struct{}
	FavoritedVideoIDs map[uint64]struct{}
	FollowedAuthorIDs map[uint64]struct{}
}

type viewerRelationMeta struct {
	FollowsFullReady     bool
	LikesRecentReady     bool
	FavoritesRecentReady bool
}

var toggleFollowRelationScript = redis.NewScript(`
local follows_key = KEYS[1]
local active_key = KEYS[2]
local meta_key = KEYS[3]

local author_id = ARGV[1]
local following = ARGV[2]
local ttl_seconds = tonumber(ARGV[3])

local current = redis.call("HGET", follows_key, author_id)
local changed = 0

if following == "1" then
	if current ~= "1" then
		changed = 1
	end
	redis.call("HSET", follows_key, author_id, "1")
else
	if current == "1" then
		changed = 1
	end
	redis.call("HSET", follows_key, author_id, "0")
end

redis.call("EXPIRE", follows_key, ttl_seconds)
redis.call("SET", active_key, "1", "EX", ttl_seconds)
redis.call("EXPIRE", meta_key, ttl_seconds)

return changed
`)

func (c *Cache) LoadViewerRelations(ctx context.Context, userID uint64, videos []ViewerRelationVideo, authorIDs []uint64) (*ViewerRelations, error) {
	result := &ViewerRelations{
		LikedVideoIDs:     make(map[uint64]struct{}),
		FavoritedVideoIDs: make(map[uint64]struct{}),
		FollowedAuthorIDs: make(map[uint64]struct{}),
	}
	if userID == 0 {
		return result, nil
	}

	meta, err := c.loadViewerRelationMeta(ctx, userID)
	if err != nil {
		return nil, err
	}

	followedAuthorIDs, err := c.loadScopedRelationState(
		ctx,
		userFollowsFullKey(userID),
		"user_follows",
		"follow_user_id",
		userID,
		authorIDs,
		meta.FollowsFullReady,
	)
	if err != nil {
		return nil, err
	}
	for authorID := range followedAuthorIDs {
		result.FollowedAuthorIDs[authorID] = struct{}{}
	}

	recentCutoff := viewerRelationRecentCutoff(time.Now().UTC())
	recentVideoIDs, oldVideoIDs := splitViewerRelationVideoIDs(videos, recentCutoff)

	likedRecentVideoIDs, err := c.loadScopedRelationState(
		ctx,
		userLikesRecentKey(userID),
		"video_likes",
		"video_id",
		userID,
		recentVideoIDs,
		meta.LikesRecentReady,
	)
	if err != nil {
		return nil, err
	}
	for videoID := range likedRecentVideoIDs {
		result.LikedVideoIDs[videoID] = struct{}{}
	}

	likedOldVideoIDs, err := c.loadScopedRelationState(
		ctx,
		userLikesOnDemandKey(userID),
		"video_likes",
		"video_id",
		userID,
		oldVideoIDs,
		false,
	)
	if err != nil {
		return nil, err
	}
	for videoID := range likedOldVideoIDs {
		result.LikedVideoIDs[videoID] = struct{}{}
	}

	favoritedRecentVideoIDs, err := c.loadScopedRelationState(
		ctx,
		userFavoritesRecentKey(userID),
		"video_favorites",
		"video_id",
		userID,
		recentVideoIDs,
		meta.FavoritesRecentReady,
	)
	if err != nil {
		return nil, err
	}
	for videoID := range favoritedRecentVideoIDs {
		result.FavoritedVideoIDs[videoID] = struct{}{}
	}

	favoritedOldVideoIDs, err := c.loadScopedRelationState(
		ctx,
		userFavoritesOnDemandKey(userID),
		"video_favorites",
		"video_id",
		userID,
		oldVideoIDs,
		false,
	)
	if err != nil {
		return nil, err
	}
	for videoID := range favoritedOldVideoIDs {
		result.FavoritedVideoIDs[videoID] = struct{}{}
	}

	return result, nil
}

func (c *Cache) LoadAllFollowedAuthorIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	if userID == 0 {
		return nil, nil
	}
	if c == nil || c.redis == nil {
		return c.queryAllFollowedAuthorIDs(ctx, userID)
	}

	meta, err := c.loadViewerRelationMeta(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !meta.FollowsFullReady {
		return c.queryAllFollowedAuthorIDs(ctx, userID)
	}

	values, err := c.redis.HGetAll(ctx, userFollowsFullKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("load all followed author ids from redis: %w", err)
	}

	authorIDs := make([]uint64, 0, len(values))
	for field, value := range values {
		if value != "1" {
			continue
		}
		authorID, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil || authorID == 0 {
			continue
		}
		authorIDs = append(authorIDs, authorID)
	}
	return authorIDs, nil
}

func (c *Cache) WarmViewerRelations(ctx context.Context, userID uint64) error {
	if c == nil || c.db == nil || c.redis == nil || userID == 0 {
		return nil
	}

	recentCutoff := viewerRelationRecentCutoff(time.Now().UTC())

	followedAuthorIDs, err := c.queryAllFollowedAuthorIDs(ctx, userID)
	if err != nil {
		return err
	}
	likedRecentVideoIDs, err := c.queryRecentVideoRelationIDs(ctx, "video_likes", userID, recentCutoff)
	if err != nil {
		return err
	}
	favoritedRecentVideoIDs, err := c.queryRecentVideoRelationIDs(ctx, "video_favorites", userID, recentCutoff)
	if err != nil {
		return err
	}

	if err := c.replaceViewerRelationCaches(ctx, userID, followedAuthorIDs, likedRecentVideoIDs, favoritedRecentVideoIDs); err != nil {
		return err
	}
	return nil
}

func (c *Cache) WarmViewerRelationsAsync(userID uint64) {
	if c == nil || userID == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), viewerRelationWarmupTimeout)
		defer cancel()

		if err := c.WarmViewerRelations(ctx, userID); err != nil {
			log.Printf("warm viewer relations failed: user_id=%d err=%v", userID, err)
		}
	}()
}

func (c *Cache) SyncLikeRelation(ctx context.Context, userID, videoID uint64, publishedAt time.Time, liked bool) error {
	if isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC())) {
		return c.syncRelationState(ctx, userLikesRecentKey(userID), videoID, liked)
	}
	return c.syncRelationState(ctx, userLikesOnDemandKey(userID), videoID, liked)
}

func (c *Cache) SyncFavoriteRelation(ctx context.Context, userID, videoID uint64, publishedAt time.Time, favorited bool) error {
	if isViewerRelationRecentVideo(publishedAt, viewerRelationRecentCutoff(time.Now().UTC())) {
		return c.syncRelationState(ctx, userFavoritesRecentKey(userID), videoID, favorited)
	}
	return c.syncRelationState(ctx, userFavoritesOnDemandKey(userID), videoID, favorited)
}

func (c *Cache) SyncFollowRelation(ctx context.Context, userID, authorID uint64, following bool) error {
	return c.syncRelationState(ctx, userFollowsFullKey(userID), authorID, following)
}

func (c *Cache) ToggleFollowRelation(ctx context.Context, userID, authorID uint64, following bool) (bool, error) {
	if c == nil || c.redis == nil || userID == 0 || authorID == 0 {
		return false, nil
	}

	followingValue := "0"
	if following {
		followingValue = "1"
	}

	result, err := toggleFollowRelationScript.Run(
		ctx,
		c.redis,
		[]string{
			userFollowsFullKey(userID),
			userActiveKey(userID),
			userRelationMetaKey(userID),
		},
		strconv.FormatUint(authorID, 10),
		followingValue,
		strconv.FormatInt(int64(FollowingActiveTTL/time.Second), 10),
	).Result()
	if err != nil {
		return false, fmt.Errorf("toggle follow relation: %w", err)
	}

	changed, ok := redisUint64Value(result)
	return ok && changed == 1, nil
}

func (c *Cache) loadViewerRelationMeta(ctx context.Context, userID uint64) (*viewerRelationMeta, error) {
	meta := &viewerRelationMeta{}
	if c == nil || c.redis == nil || userID == 0 {
		return meta, nil
	}

	values, err := c.redis.HMGet(
		ctx,
		userRelationMetaKey(userID),
		relationMetaFollowsFullReadyField,
		relationMetaLikesRecentReadyField,
		relationMetaFavoritesRecentReadyField,
	).Result()
	if err != nil {
		return nil, fmt.Errorf("load viewer relation meta: %w", err)
	}

	meta.FollowsFullReady = redisMetaValuePresent(values, 0)
	meta.LikesRecentReady = redisMetaValuePresent(values, 1)
	meta.FavoritesRecentReady = redisMetaValuePresent(values, 2)
	return meta, nil
}

func (c *Cache) loadScopedRelationState(ctx context.Context, redisKey, tableName, relationColumn string, userID uint64, ids []uint64, ready bool) (map[uint64]struct{}, error) {
	result := make(map[uint64]struct{})
	if userID == 0 || len(ids) == 0 {
		return result, nil
	}
	if c == nil || c.redis == nil {
		return c.queryRelationState(ctx, tableName, relationColumn, userID, ids)
	}

	loaded, missing, err := c.loadRelationStateFromRedis(ctx, redisKey, ids)
	if err != nil {
		return nil, fmt.Errorf("load relation state from redis %q: %w", redisKey, err)
	}
	for id := range loaded {
		result[id] = struct{}{}
	}

	if ready || len(missing) == 0 {
		return result, nil
	}

	queried, err := c.queryRelationState(ctx, tableName, relationColumn, userID, missing)
	if err != nil {
		return nil, err
	}
	for id := range queried {
		result[id] = struct{}{}
	}

	if err := c.writeQueriedRelationState(ctx, redisKey, missing, queried); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Cache) loadRelationStateFromRedis(ctx context.Context, redisKey string, ids []uint64) (map[uint64]struct{}, []uint64, error) {
	result := make(map[uint64]struct{})
	if len(ids) == 0 {
		return result, nil, nil
	}

	fields := idsToFields(ids)
	values, err := c.redis.HMGet(ctx, redisKey, fields...).Result()
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

		switch raw {
		case "1":
			result[id] = struct{}{}
		case "0":
		default:
			missing = append(missing, id)
		}
	}

	return result, missing, nil
}

func (c *Cache) queryRelationState(ctx context.Context, tableName, relationColumn string, userID uint64, ids []uint64) (map[uint64]struct{}, error) {
	result := make(map[uint64]struct{})
	if len(ids) == 0 {
		return result, nil
	}
	if c == nil || c.db == nil {
		return result, fmt.Errorf("feed cache database is not initialized")
	}

	var values []uint64
	if err := c.db.WithContext(ctx).
		Table(tableName).
		Select(relationColumn).
		Where("user_id = ? AND "+relationColumn+" IN ?", userID, ids).
		Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("query relation state from %s: %w", tableName, err)
	}

	for _, value := range values {
		result[value] = struct{}{}
	}
	return result, nil
}

func (c *Cache) writeQueriedRelationState(ctx context.Context, redisKey string, ids []uint64, loaded map[uint64]struct{}) error {
	if c == nil || c.redis == nil || len(ids) == 0 {
		return nil
	}

	cacheValues := make(map[string]interface{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		cacheValues[strconv.FormatUint(id, 10)] = "0"
	}
	for id := range loaded {
		cacheValues[strconv.FormatUint(id, 10)] = "1"
	}

	if len(cacheValues) == 0 {
		return nil
	}
	pipe := c.redis.Pipeline()
	pipe.HSet(ctx, redisKey, cacheValues)
	pipe.Expire(ctx, redisKey, FollowingActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store relation state in redis %q: %w", redisKey, err)
	}
	return nil
}

func (c *Cache) replaceViewerRelationCaches(ctx context.Context, userID uint64, followedAuthorIDs, likedVideoIDs, favoritedVideoIDs []uint64) error {
	if c == nil || c.redis == nil || userID == 0 {
		return nil
	}

	pullModeAuthorSet, err := c.LoadAuthorPullModeIDs(ctx, followedAuthorIDs)
	if err != nil {
		return err
	}
	followedPullAuthorIDs := make([]uint64, 0, len(pullModeAuthorSet))
	for authorID := range pullModeAuthorSet {
		followedPullAuthorIDs = append(followedPullAuthorIDs, authorID)
	}

	readyAt := time.Now().UTC().Format(time.RFC3339Nano)
	pipe := c.redis.TxPipeline()
	pipe.Del(
		ctx,
		userFollowsFullKey(userID),
		userLikesRecentKey(userID),
		userFavoritesRecentKey(userID),
		followedPullAuthorsKey(userID),
	)

	if values := relationFieldsWithValue(followedAuthorIDs, "1"); len(values) > 0 {
		pipe.HSet(ctx, userFollowsFullKey(userID), values)
	}
	if values := relationFieldsWithValue(likedVideoIDs, "1"); len(values) > 0 {
		pipe.HSet(ctx, userLikesRecentKey(userID), values)
	}
	if values := relationFieldsWithValue(favoritedVideoIDs, "1"); len(values) > 0 {
		pipe.HSet(ctx, userFavoritesRecentKey(userID), values)
	}

	pipe.HSet(ctx, userRelationMetaKey(userID), map[string]interface{}{
		relationMetaFollowsFullReadyField:     readyAt,
		relationMetaLikesRecentReadyField:     readyAt,
		relationMetaFavoritesRecentReadyField: readyAt,
	})
	addFollowedPullAuthorsReplaceToPipeline(ctx, pipe, userID, followedPullAuthorIDs)
	addUserRelationTTLToPipeline(ctx, pipe, userID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("replace viewer relation caches: %w", err)
	}
	return nil
}

func (c *Cache) queryAllFollowedAuthorIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("feed cache database is not initialized")
	}

	var authorIDs []uint64
	if err := c.db.WithContext(ctx).
		Table("user_follows").
		Select("follow_user_id").
		Where("user_id = ?", userID).
		Scan(&authorIDs).Error; err != nil {
		return nil, fmt.Errorf("query all followed author ids: %w", err)
	}
	return authorIDs, nil
}

func (c *Cache) queryRecentVideoRelationIDs(ctx context.Context, tableName string, userID uint64, recentCutoff time.Time) ([]uint64, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("feed cache database is not initialized")
	}

	var videoIDs []uint64
	if err := c.db.WithContext(ctx).
		Table(tableName+" AS rel").
		Select("rel.video_id").
		Joins("JOIN videos AS v ON v.id = rel.video_id").
		Where("rel.user_id = ?", userID).
		Where("rel.created_at >= ?", recentCutoff.UTC()).
		Where("v.status = ?", model.VideoStatusPublished).
		Where("v.deleted_at IS NULL").
		Where("v.published_at >= ?", recentCutoff.UTC()).
		Scan(&videoIDs).Error; err != nil {
		return nil, fmt.Errorf("query recent relation ids from %s: %w", tableName, err)
	}
	return videoIDs, nil
}

func (c *Cache) syncRelationState(ctx context.Context, redisKey string, id uint64, active bool) error {
	if c == nil || c.redis == nil || id == 0 {
		return nil
	}

	value := "0"
	if active {
		value = "1"
	}

	pipe := c.redis.Pipeline()
	pipe.HSet(ctx, redisKey, strconv.FormatUint(id, 10), value)
	pipe.Expire(ctx, redisKey, FollowingActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("sync relation state in redis %q: %w", redisKey, err)
	}
	return nil
}

func relationFieldsWithValue(ids []uint64, value string) map[string]interface{} {
	fields := make(map[string]interface{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		fields[strconv.FormatUint(id, 10)] = value
	}
	return fields
}

func splitViewerRelationVideoIDs(videos []ViewerRelationVideo, recentCutoff time.Time) ([]uint64, []uint64) {
	recentVideoIDs := make([]uint64, 0, len(videos))
	oldVideoIDs := make([]uint64, 0, len(videos))
	seenRecent := make(map[uint64]struct{}, len(videos))
	seenOld := make(map[uint64]struct{}, len(videos))
	for _, video := range videos {
		if video.VideoID == 0 {
			continue
		}
		if isViewerRelationRecentVideo(video.PublishedAt, recentCutoff) {
			if _, exists := seenRecent[video.VideoID]; exists {
				continue
			}
			seenRecent[video.VideoID] = struct{}{}
			recentVideoIDs = append(recentVideoIDs, video.VideoID)
			continue
		}

		if _, exists := seenOld[video.VideoID]; exists {
			continue
		}
		seenOld[video.VideoID] = struct{}{}
		oldVideoIDs = append(oldVideoIDs, video.VideoID)
	}
	return recentVideoIDs, oldVideoIDs
}

func viewerRelationRecentCutoff(now time.Time) time.Time {
	return now.UTC().Add(-viewerRelationRecentWindow)
}

func isViewerRelationRecentVideo(publishedAt, recentCutoff time.Time) bool {
	return !publishedAt.UTC().Before(recentCutoff.UTC())
}

func redisMetaValuePresent(values []interface{}, index int) bool {
	if index < 0 || index >= len(values) || values[index] == nil {
		return false
	}

	raw, ok := redisValueToString(values[index])
	return ok && raw != ""
}

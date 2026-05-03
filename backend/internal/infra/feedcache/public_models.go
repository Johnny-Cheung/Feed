package feedcache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
)

type visibleAuthorRow struct {
	AuthorID uint64 `gorm:"column:author_id"`
}

func (c *Cache) LoadVideoBasesByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]*VideoBase, []uint64, error) {
	loaded, missing, err := c.loadJSONMapByUint64Keys(ctx, keysForVideoBaseIDs(videoIDs), videoIDs, decodeVideoBase)
	if err != nil {
		return nil, nil, fmt.Errorf("load video bases: %w", err)
	}

	result := make(map[uint64]*VideoBase, len(loaded))
	for videoID, value := range loaded {
		base, ok := value.(*VideoBase)
		if !ok || base == nil {
			missing = append(missing, videoID)
			continue
		}
		result[videoID] = base
	}

	return result, missing, nil
}

func (c *Cache) BuildVideoBasesByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]*VideoBase, error) {
	result := make(map[uint64]*VideoBase, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("feed cache database is not initialized")
	}

	var videos []model.Video
	if err := c.db.WithContext(ctx).
		Where("id IN ? AND status = ?", videoIDs, model.VideoStatusPublished).
		Where("deleted_at IS NULL").
		Find(&videos).Error; err != nil {
		return nil, fmt.Errorf("build video bases: %w", err)
	}

	for i := range videos {
		video := videos[i]
		result[video.ID] = NewVideoBase(&video)
	}

	return result, nil
}

func (c *Cache) StoreVideoBase(ctx context.Context, base *VideoBase) error {
	if base == nil {
		return nil
	}
	return c.StoreVideoBases(ctx, map[uint64]*VideoBase{base.VideoID: base})
}

func (c *Cache) StoreVideoBases(ctx context.Context, bases map[uint64]*VideoBase) error {
	if len(bases) == 0 || c == nil || c.redis == nil {
		return nil
	}

	keys := make([]string, 0, len(bases))
	payloads := make([][]byte, 0, len(bases))
	for videoID, base := range bases {
		if videoID == 0 || base == nil {
			continue
		}
		base.PublishedAt = base.PublishedAt.UTC().Round(0)
		payload, err := json.Marshal(base)
		if err != nil {
			return fmt.Errorf("marshal video base: %w", err)
		}
		keys = append(keys, videoBaseKey(videoID))
		payloads = append(payloads, payload)
	}

	if err := c.storeJSONMap(ctx, keys, payloads); err != nil {
		return fmt.Errorf("store video bases: %w", err)
	}
	return nil
}

func (c *Cache) RefreshVideoBasesByVideoIDs(ctx context.Context, videoIDs []uint64) error {
	if len(videoIDs) == 0 {
		return nil
	}

	bases, err := c.BuildVideoBasesByVideoIDs(ctx, videoIDs)
	if err != nil {
		return err
	}
	if err := c.StoreVideoBases(ctx, bases); err != nil {
		return err
	}

	missing := idsMissingFromMap(videoIDs, bases)
	if len(missing) == 0 {
		return nil
	}
	return c.DeleteVideoBasesByVideoIDs(ctx, missing)
}

func (c *Cache) DeleteVideoBasesByVideoIDs(ctx context.Context, videoIDs []uint64) error {
	if len(videoIDs) == 0 || c == nil || c.redis == nil {
		return nil
	}

	if err := c.redis.Del(ctx, keysForVideoBaseIDs(videoIDs)...).Err(); err != nil {
		return fmt.Errorf("delete video bases: %w", err)
	}
	return nil
}

func (c *Cache) LoadUserBriefsByUserIDs(ctx context.Context, userIDs []uint64) (map[uint64]*UserBrief, []uint64, error) {
	loaded, missing, err := c.loadJSONMapByUint64Keys(ctx, keysForUserIDs(userIDs), userIDs, decodeUserBrief)
	if err != nil {
		return nil, nil, fmt.Errorf("load user briefs: %w", err)
	}

	result := make(map[uint64]*UserBrief, len(loaded))
	for userID, value := range loaded {
		brief, ok := value.(*UserBrief)
		if !ok || brief == nil {
			missing = append(missing, userID)
			continue
		}
		result[userID] = brief
	}

	return result, missing, nil
}

func (c *Cache) BuildUserBriefsByUserIDs(ctx context.Context, userIDs []uint64) (map[uint64]*UserBrief, error) {
	result := make(map[uint64]*UserBrief, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("feed cache database is not initialized")
	}

	var users []model.User
	if err := c.db.WithContext(ctx).Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("build user briefs: %w", err)
	}

	for i := range users {
		user := users[i]
		result[user.ID] = NewUserBrief(&user)
	}

	return result, nil
}

func (c *Cache) StoreUserBrief(ctx context.Context, brief *UserBrief) error {
	if brief == nil {
		return nil
	}
	return c.StoreUserBriefs(ctx, map[uint64]*UserBrief{brief.UserID: brief})
}

func (c *Cache) StoreUserBriefs(ctx context.Context, briefs map[uint64]*UserBrief) error {
	if len(briefs) == 0 || c == nil || c.redis == nil {
		return nil
	}

	keys := make([]string, 0, len(briefs))
	payloads := make([][]byte, 0, len(briefs))
	for userID, brief := range briefs {
		if userID == 0 || brief == nil {
			continue
		}
		payload, err := json.Marshal(brief)
		if err != nil {
			return fmt.Errorf("marshal user brief: %w", err)
		}
		keys = append(keys, userBriefKey(userID))
		payloads = append(payloads, payload)
	}

	if err := c.storeJSONMap(ctx, keys, payloads); err != nil {
		return fmt.Errorf("store user briefs: %w", err)
	}
	return nil
}

func (c *Cache) LoadVideoStatsByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]*VideoStats, []uint64, error) {
	result := make(map[uint64]*VideoStats, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil, nil
	}
	if c == nil || c.redis == nil {
		return result, append([]uint64(nil), videoIDs...), nil
	}

	pipe := c.redis.Pipeline()
	cmds := make([]*redis.SliceCmd, 0, len(videoIDs))
	filteredVideoIDs := make([]uint64, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		filteredVideoIDs = append(filteredVideoIDs, videoID)
		cmds = append(cmds, pipe.HMGet(
			ctx,
			videoStatsKey(videoID),
			videoStatsVideoIDField,
			videoStatsLikeCountField,
			videoStatsCommentCountField,
			videoStatsFavoriteCountField,
			videoStatsHotScoreField,
		))
	}
	if len(cmds) == 0 {
		return result, nil, nil
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, nil, fmt.Errorf("load video stats cache: %w", err)
	}

	missing := make([]uint64, 0)
	for i, cmd := range cmds {
		videoID := filteredVideoIDs[i]
		values, err := cmd.Result()
		if err != nil {
			return nil, nil, fmt.Errorf("load video stats cache: video_id=%d err=%w", videoID, err)
		}

		stats, ok := parseVideoStatsHash(videoID, values)
		if !ok {
			missing = append(missing, videoID)
			continue
		}
		result[videoID] = stats
	}

	return result, missing, nil
}

func (c *Cache) BuildVideoStatsByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]*VideoStats, error) {
	result := make(map[uint64]*VideoStats, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("feed cache database is not initialized")
	}

	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		result[videoID] = &VideoStats{VideoID: videoID}
	}

	var statsRows []model.VideoStats
	if err := c.db.WithContext(ctx).Where("video_id IN ?", videoIDs).Find(&statsRows).Error; err != nil {
		return nil, fmt.Errorf("build video stats cache: %w", err)
	}

	for i := range statsRows {
		stats := statsRows[i]
		result[stats.VideoID] = NewVideoStats(&stats)
	}

	return result, nil
}

func (c *Cache) StoreVideoStats(ctx context.Context, stats *VideoStats) error {
	if stats == nil {
		return nil
	}
	return c.StoreVideoStatsBatch(ctx, map[uint64]*VideoStats{stats.VideoID: stats})
}

func (c *Cache) StoreVideoStatsBatch(ctx context.Context, statsMap map[uint64]*VideoStats) error {
	if len(statsMap) == 0 || c == nil || c.redis == nil {
		return nil
	}

	pipe := c.redis.Pipeline()
	for videoID, stats := range statsMap {
		if videoID == 0 || stats == nil {
			continue
		}
		pipe.HSet(ctx, videoStatsKey(videoID), map[string]interface{}{
			videoStatsVideoIDField:       videoID,
			videoStatsLikeCountField:     stats.LikeCount,
			videoStatsCommentCountField:  stats.CommentCount,
			videoStatsFavoriteCountField: stats.FavoriteCount,
			videoStatsHotScoreField:      stats.HotScore,
		})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store video stats cache: %w", err)
	}
	return nil
}

func (c *Cache) RefreshVideoStatsByVideoIDs(ctx context.Context, videoIDs []uint64) error {
	if len(videoIDs) == 0 {
		return nil
	}

	statsMap, err := c.BuildVideoStatsByVideoIDs(ctx, videoIDs)
	if err != nil {
		return err
	}
	return c.StoreVideoStatsBatch(ctx, statsMap)
}

func (c *Cache) DeleteVideoStatsByVideoIDs(ctx context.Context, videoIDs []uint64) error {
	if len(videoIDs) == 0 || c == nil || c.redis == nil {
		return nil
	}

	keys := keysForVideoStatsIDs(videoIDs)
	keys = append(keys, legacyKeysForVideoStatsIDs(videoIDs)...)
	if err := c.redis.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("delete video stats cache: %w", err)
	}
	return nil
}

func (c *Cache) RebuildAllVideoBases(ctx context.Context, batchSize int) (int, error) {
	if c == nil || c.redis == nil {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	if err := c.clearByPrefix(ctx, videoBaseKeyPrefix); err != nil {
		return 0, err
	}

	var lastID uint64
	updated := 0
	for {
		videoIDs, err := c.listVisibleVideoIDsAfterID(ctx, lastID, batchSize)
		if err != nil {
			return updated, err
		}
		if len(videoIDs) == 0 {
			return updated, nil
		}

		bases, err := c.BuildVideoBasesByVideoIDs(ctx, videoIDs)
		if err != nil {
			return updated, err
		}
		if err := c.StoreVideoBases(ctx, bases); err != nil {
			return updated, err
		}

		updated += len(bases)
		lastID = videoIDs[len(videoIDs)-1]
		if len(videoIDs) < batchSize {
			return updated, nil
		}
	}
}

func (c *Cache) RebuildAllVisibleUserBriefs(ctx context.Context, batchSize int) (int, error) {
	if c == nil || c.redis == nil {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	if err := c.clearByPrefix(ctx, userBriefKeyPrefix); err != nil {
		return 0, err
	}

	var lastAuthorID uint64
	updated := 0
	for {
		authorIDs, err := c.listVisibleAuthorIDsAfterID(ctx, lastAuthorID, batchSize)
		if err != nil {
			return updated, err
		}
		if len(authorIDs) == 0 {
			return updated, nil
		}

		briefs, err := c.BuildUserBriefsByUserIDs(ctx, authorIDs)
		if err != nil {
			return updated, err
		}
		if err := c.StoreUserBriefs(ctx, briefs); err != nil {
			return updated, err
		}

		updated += len(briefs)
		lastAuthorID = authorIDs[len(authorIDs)-1]
		if len(authorIDs) < batchSize {
			return updated, nil
		}
	}
}

func (c *Cache) RebuildAllVideoStats(ctx context.Context, batchSize int) (int, error) {
	if c == nil || c.redis == nil {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	if err := c.clearByPrefix(ctx, videoStatsKeyPrefix); err != nil {
		return 0, err
	}
	if err := c.clearByPrefix(ctx, legacyVideoStatsKeyPrefix); err != nil {
		return 0, err
	}

	var lastID uint64
	updated := 0
	for {
		videoIDs, err := c.listVisibleVideoIDsAfterID(ctx, lastID, batchSize)
		if err != nil {
			return updated, err
		}
		if len(videoIDs) == 0 {
			return updated, nil
		}

		statsMap, err := c.BuildVideoStatsByVideoIDs(ctx, videoIDs)
		if err != nil {
			return updated, err
		}
		if err := c.StoreVideoStatsBatch(ctx, statsMap); err != nil {
			return updated, err
		}

		updated += len(statsMap)
		lastID = videoIDs[len(videoIDs)-1]
		if len(videoIDs) < batchSize {
			return updated, nil
		}
	}
}

func parseVideoStatsHash(expectedVideoID uint64, values []interface{}) (*VideoStats, bool) {
	if len(values) < 5 || redisHashValuesEmpty(values) {
		return nil, false
	}

	videoID := expectedVideoID
	if parsedVideoID, ok := redisUint64Value(values[0]); ok && parsedVideoID != 0 {
		videoID = parsedVideoID
	}
	if videoID != expectedVideoID || videoID == 0 {
		return nil, false
	}

	likeCount, _ := redisUint32Value(values[1])
	commentCount, _ := redisUint32Value(values[2])
	favoriteCount, _ := redisUint32Value(values[3])
	hotScore, _ := redisFloat64Value(values[4])

	return &VideoStats{
		VideoID:       videoID,
		LikeCount:     likeCount,
		CommentCount:  commentCount,
		FavoriteCount: favoriteCount,
		HotScore:      hotScore,
	}, true
}

func redisHashValuesEmpty(values []interface{}) bool {
	for _, value := range values {
		if value != nil {
			return false
		}
	}
	return true
}

func redisUint64Value(value interface{}) (uint64, bool) {
	raw, ok := redisValueToString(value)
	if !ok || raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	return parsed, err == nil
}

func redisUint32Value(value interface{}) (uint32, bool) {
	parsed, ok := redisUint64Value(value)
	if !ok {
		return 0, false
	}
	if parsed > uint64(^uint32(0)) {
		return ^uint32(0), true
	}
	return uint32(parsed), true
}

func redisFloat64Value(value interface{}) (float64, bool) {
	raw, ok := redisValueToString(value)
	if !ok || raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	return parsed, err == nil
}

func (c *Cache) listVisibleVideoIDsAfterID(ctx context.Context, lastID uint64, limit int) ([]uint64, error) {
	if limit <= 0 {
		return nil, nil
	}

	var videoIDs []uint64
	if err := c.db.WithContext(ctx).
		Model(&model.Video{}).
		Select("id").
		Where("id > ? AND status = ?", lastID, model.VideoStatusPublished).
		Where("deleted_at IS NULL").
		Order("id ASC").
		Limit(limit).
		Scan(&videoIDs).Error; err != nil {
		return nil, fmt.Errorf("list visible video ids after id: %w", err)
	}
	return videoIDs, nil
}

func (c *Cache) listVisibleAuthorIDsAfterID(ctx context.Context, lastAuthorID uint64, limit int) ([]uint64, error) {
	if limit <= 0 {
		return nil, nil
	}

	var rows []visibleAuthorRow
	if err := c.db.WithContext(ctx).
		Table("videos").
		Select("DISTINCT author_id").
		Where("author_id > ? AND status = ?", lastAuthorID, model.VideoStatusPublished).
		Where("deleted_at IS NULL").
		Order("author_id ASC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list visible author ids after id: %w", err)
	}

	authorIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		if row.AuthorID == 0 {
			continue
		}
		authorIDs = append(authorIDs, row.AuthorID)
	}
	return authorIDs, nil
}

func (c *Cache) clearByPrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		keys, nextCursor, err := c.redis.Scan(ctx, cursor, prefix+"*", scanCount).Result()
		if err != nil {
			return fmt.Errorf("scan redis keys by prefix %q: %w", prefix, err)
		}
		if len(keys) > 0 {
			if err := c.redis.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete redis keys by prefix %q: %w", prefix, err)
			}
		}
		if nextCursor == 0 {
			return nil
		}
		cursor = nextCursor
	}
}

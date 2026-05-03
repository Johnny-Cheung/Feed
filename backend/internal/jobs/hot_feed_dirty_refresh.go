package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"feed-backend/internal/common/hotscore"
	"feed-backend/internal/infra/feedcache"
	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
)

const hotFeedDirtyRefreshJobName = "hot_feed_dirty_refresh"

func (s *Service) RunHotFeedDirtyRefresh(ctx context.Context) (result JobResult, err error) {
	result = JobResult{
		Name:      hotFeedDirtyRefreshJobName,
		StartedAt: time.Now().UTC(),
	}
	defer result.finish()

	if s.redis == nil {
		result.Skipped = true
		result.SkipReason = "redis not initialized"
		return result, nil
	}

	lock, acquired, err := acquireRedisLock(ctx, s.redis, hotFeedDirtyRefreshLockKey, s.options.LockTTL)
	if err != nil {
		return result, err
	}
	if !acquired {
		result.Skipped = true
		result.SkipReason = "lock held"
		return result, nil
	}
	defer func() {
		if releaseErr := releaseLock(lock); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	batchSize := s.options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultJobBatchSize
	}

	for {
		rawIDs, popErr := s.redis.SPopN(ctx, feedcache.HomeHotDirtyKey(), int64(batchSize)).Result()
		if errors.Is(popErr, redis.Nil) || len(rawIDs) == 0 {
			return result, nil
		}
		if popErr != nil {
			return result, fmt.Errorf("pop dirty hot feed video ids: %w", popErr)
		}

		videoIDs := parseDirtyVideoIDs(rawIDs)
		if len(videoIDs) == 0 {
			continue
		}

		result.Scanned += len(videoIDs)
		updatedStats, updatedCacheEntries, redisMembers, refreshErr := s.refreshDirtyHotFeedBatch(ctx, videoIDs)
		if refreshErr != nil {
			s.requeueDirtyVideoIDsBestEffort(ctx, videoIDs)
			return result, refreshErr
		}

		result.UpdatedStats += updatedStats
		result.UpdatedCacheEntries += updatedCacheEntries
		result.RedisMembers += redisMembers

		if len(rawIDs) < batchSize {
			return result, nil
		}
	}
}

func (s *Service) refreshDirtyHotFeedBatch(ctx context.Context, videoIDs []uint64) (int, int, int, error) {
	videos, err := s.repo.LoadVisibleVideosByIDs(ctx, videoIDs)
	if err != nil {
		return 0, 0, 0, err
	}

	visibleVideoIDs := make([]uint64, 0, len(videos))
	removeVideoIDs := make([]uint64, 0, len(videoIDs)-len(videos))
	for _, videoID := range videoIDs {
		if _, ok := videos[videoID]; ok {
			visibleVideoIDs = append(visibleVideoIDs, videoID)
			continue
		}
		removeVideoIDs = append(removeVideoIDs, videoID)
	}

	statsMap, err := s.loadHotFeedStats(ctx, visibleVideoIDs)
	if err != nil {
		return 0, 0, 0, err
	}

	refs := make([]HotFeedRef, 0, len(visibleVideoIDs))
	statsRows := make([]model.VideoStats, 0, len(visibleVideoIDs))
	cacheStats := make(map[uint64]*feedcache.VideoStats, len(visibleVideoIDs))
	for _, videoID := range visibleVideoIDs {
		video := videos[videoID]
		stats := statsMap[videoID]
		if stats == nil {
			stats = &feedcache.VideoStats{VideoID: videoID}
		}

		hotScore := hotscore.Calculate(video.PublishedAt, stats.LikeCount, stats.CommentCount, stats.FavoriteCount)
		statsRows = append(statsRows, model.VideoStats{
			VideoID:       videoID,
			LikeCount:     stats.LikeCount,
			CommentCount:  stats.CommentCount,
			FavoriteCount: stats.FavoriteCount,
			HotScore:      hotScore,
		})
		cacheStats[videoID] = &feedcache.VideoStats{
			VideoID:       videoID,
			LikeCount:     stats.LikeCount,
			CommentCount:  stats.CommentCount,
			FavoriteCount: stats.FavoriteCount,
			HotScore:      hotScore,
		}
		refs = append(refs, HotFeedRef{
			VideoID:  videoID,
			HotScore: hotScore,
		})
	}

	updatedStats, err := s.repo.UpsertVideoStatsBatch(ctx, statsRows)
	if err != nil {
		return 0, 0, 0, err
	}

	updatedCacheEntries := 0
	if len(cacheStats) > 0 {
		if err := s.cache.StoreVideoStatsBatch(ctx, cacheStats); err != nil {
			return 0, 0, 0, err
		}
		updatedCacheEntries = len(cacheStats)
	}

	redisMembers, err := s.repo.UpdateHomeHotFeed(ctx, refs, removeVideoIDs, s.options.HomeHotMaxEntries)
	if err != nil {
		return 0, 0, 0, err
	}

	return updatedStats, updatedCacheEntries, redisMembers, nil
}

func (s *Service) loadHotFeedStats(ctx context.Context, videoIDs []uint64) (map[uint64]*feedcache.VideoStats, error) {
	result := make(map[uint64]*feedcache.VideoStats, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}

	missing := append([]uint64(nil), videoIDs...)
	if s.cache != nil {
		loaded, cacheMissing, err := s.cache.LoadVideoStatsByVideoIDs(ctx, videoIDs)
		if err != nil {
			log.Printf("load dirty hot feed stats from redis failed, fallback to mysql counts: err=%v", err)
		} else {
			for videoID, stats := range loaded {
				result[videoID] = stats
			}
			missing = cacheMissing
		}
	}

	if len(missing) == 0 {
		return result, nil
	}

	counts, err := s.repo.LoadVideoStatsCounts(ctx, missing)
	if err != nil {
		return nil, err
	}
	for _, videoID := range missing {
		current := counts[videoID]
		result[videoID] = &feedcache.VideoStats{
			VideoID:       videoID,
			LikeCount:     current.LikeCount,
			CommentCount:  current.CommentCount,
			FavoriteCount: current.FavoriteCount,
		}
	}
	return result, nil
}

func parseDirtyVideoIDs(rawIDs []string) []uint64 {
	videoIDs := make([]uint64, 0, len(rawIDs))
	seen := make(map[uint64]struct{}, len(rawIDs))
	for _, rawID := range rawIDs {
		videoID, err := strconv.ParseUint(rawID, 10, 64)
		if err != nil || videoID == 0 {
			continue
		}
		if _, ok := seen[videoID]; ok {
			continue
		}
		seen[videoID] = struct{}{}
		videoIDs = append(videoIDs, videoID)
	}
	return videoIDs
}

func (s *Service) requeueDirtyVideoIDsBestEffort(ctx context.Context, videoIDs []uint64) {
	if s.cache == nil || len(videoIDs) == 0 {
		return
	}
	if err := s.cache.MarkHomeHotDirty(ctx, videoIDs...); err != nil {
		log.Printf("requeue dirty hot feed video ids failed: err=%v", err)
	}
}

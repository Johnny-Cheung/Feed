package jobs

import (
	"context"
	"time"
)

const hotFeedRebuildJobName = "hot_feed_rebuild"

func (s *Service) RunHotFeedRebuild(ctx context.Context) (result JobResult, err error) {
	result = JobResult{
		Name:      hotFeedRebuildJobName,
		StartedAt: time.Now().UTC(),
	}
	defer result.finish()

	lock, acquired, err := acquireRedisLock(ctx, s.redis, hotFeedRebuildLockKey, s.options.LockTTL)
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

	refs, scanned, updated, err := s.recomputeVisibleVideoStats(ctx)
	if err != nil {
		return result, err
	}

	redisMembers, err := s.repo.ReplaceHomeHotFeed(ctx, refs, s.options.HomeHotMaxEntries)
	if err != nil {
		return result, err
	}

	updatedVideoBases, err := s.cache.RebuildAllVideoBases(ctx, s.options.BatchSize)
	if err != nil {
		return result, err
	}

	updatedUserBriefs, err := s.cache.RebuildAllVisibleUserBriefs(ctx, s.options.BatchSize)
	if err != nil {
		return result, err
	}

	updatedVideoStats, err := s.cache.RebuildAllVideoStats(ctx, s.options.BatchSize)
	if err != nil {
		return result, err
	}

	result.Scanned = scanned
	result.UpdatedStats = updated
	result.UpdatedCacheEntries = updatedVideoBases + updatedUserBriefs + updatedVideoStats
	result.RedisMembers = redisMembers
	return result, nil
}

package jobs

import (
	"context"
	"fmt"
	"time"

	"feed-backend/internal/common/hotscore"
	"feed-backend/internal/infra/feedcache"
	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const videoStatsReconcileJobName = "video_stats_reconcile"

type Service struct {
	repo    *Repository
	redis   *redis.Client
	cache   *feedcache.Cache
	options SchedulerOptions
}

func NewService(db *gorm.DB, redisClient *redis.Client, options SchedulerOptions) *Service {
	return &Service{
		repo:    NewRepository(db, redisClient),
		redis:   redisClient,
		cache:   feedcache.NewCache(db, redisClient),
		options: normalizeOptions(options),
	}
}

func (s *Service) RunVideoStatsReconcile(ctx context.Context) (result JobResult, err error) {
	result = JobResult{
		Name:      videoStatsReconcileJobName,
		StartedAt: time.Now().UTC(),
	}
	defer result.finish()

	lock, acquired, err := acquireRedisLock(ctx, s.redis, videoStatsReconcileLockKey, s.options.LockTTL)
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

	result.Scanned = scanned
	result.UpdatedStats = updated
	result.UpdatedCacheEntries, err = s.refreshVideoStatsCache(ctx, refs)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) recomputeVisibleVideoStats(ctx context.Context) ([]HotFeedRef, int, int, error) {
	batchSize := s.options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultJobBatchSize
	}

	var lastID uint64
	var scanned int
	var updated int
	refs := make([]HotFeedRef, 0)

	for {
		videos, err := s.repo.ListVisibleVideosAfterID(ctx, lastID, batchSize)
		if err != nil {
			return nil, scanned, updated, err
		}
		if len(videos) == 0 {
			return refs, scanned, updated, nil
		}

		videoIDs := make([]uint64, 0, len(videos))
		for _, video := range videos {
			videoIDs = append(videoIDs, video.ID)
		}

		counts, err := s.repo.LoadVideoStatsCounts(ctx, videoIDs)
		if err != nil {
			return nil, scanned, updated, err
		}

		statsRows := make([]model.VideoStats, 0, len(videos))
		for _, video := range videos {
			current := counts[video.ID]
			hotScore := hotscore.Calculate(video.PublishedAt, current.LikeCount, current.CommentCount, current.FavoriteCount)
			statsRows = append(statsRows, model.VideoStats{
				VideoID:       video.ID,
				LikeCount:     current.LikeCount,
				CommentCount:  current.CommentCount,
				FavoriteCount: current.FavoriteCount,
				HotScore:      hotScore,
			})
			refs = append(refs, HotFeedRef{
				VideoID:  video.ID,
				HotScore: hotScore,
			})
		}

		updatedBatch, err := s.repo.UpsertVideoStatsBatch(ctx, statsRows)
		if err != nil {
			return nil, scanned, updated, err
		}

		scanned += len(videos)
		updated += updatedBatch
		lastID = videos[len(videos)-1].ID
		if len(videos) < batchSize {
			return refs, scanned, updated, nil
		}
	}
}

func releaseLock(lock *redisLock) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Release(ctx); err != nil {
		return fmt.Errorf("release job lock: %w", err)
	}
	return nil
}

func (s *Service) refreshVideoStatsCache(ctx context.Context, refs []HotFeedRef) (int, error) {
	if len(refs) == 0 || s.cache == nil || s.redis == nil {
		return 0, nil
	}

	batchSize := s.options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultJobBatchSize
	}

	videoIDs := make([]uint64, 0, len(refs))
	updated := 0
	for _, ref := range refs {
		videoIDs = append(videoIDs, ref.VideoID)
	}

	for start := 0; start < len(videoIDs); start += batchSize {
		end := start + batchSize
		if end > len(videoIDs) {
			end = len(videoIDs)
		}

		chunk := videoIDs[start:end]
		if err := s.cache.RefreshVideoStatsByVideoIDs(ctx, chunk); err != nil {
			return updated, err
		}
		updated += len(chunk)
	}

	return updated, nil
}

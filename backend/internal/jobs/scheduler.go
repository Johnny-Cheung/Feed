package jobs

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	defaultVideoStatsReconcileInterval = 24 * time.Hour
	defaultHotFeedDirtyRefreshInterval = time.Minute
	defaultHotFeedRebuildInterval      = time.Hour
	defaultJobLockTTL                  = 30 * time.Minute
	defaultJobBatchSize                = 500
	defaultHomeHotMaxEntries           = 10000
)

type Scheduler struct {
	service *Service
	options SchedulerOptions

	ctx    context.Context
	cancel context.CancelFunc

	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

func NewScheduler(db *gorm.DB, redisClient *redis.Client, options SchedulerOptions) *Scheduler {
	normalized := normalizeOptions(options)
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		service: NewService(db, redisClient, normalized),
		options: normalized,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (s *Scheduler) Start() {
	if s == nil {
		return
	}

	s.startOnce.Do(func() {
		if !s.options.Enabled {
			log.Println("jobs scheduler disabled")
			return
		}

		s.startLoop(videoStatsReconcileJobName, s.options.VideoStatsReconcileInterval, s.service.RunVideoStatsReconcile)
		s.startLoop(hotFeedDirtyRefreshJobName, s.options.HotFeedDirtyRefreshInterval, s.service.RunHotFeedDirtyRefresh)
		s.startLoop(hotFeedRebuildJobName, s.options.HotFeedRebuildInterval, s.service.RunHotFeedRebuild)
	})
}

func (s *Scheduler) Close() {
	if s == nil {
		return
	}

	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
	})
}

func (s *Scheduler) startLoop(name string, interval time.Duration, run func(context.Context) (JobResult, error)) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		if s.options.RunOnStart {
			s.runOnce(name, run)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(name, run)
			}
		}
	}()
}

func (s *Scheduler) runOnce(name string, run func(context.Context) (JobResult, error)) {
	log.Printf("job %s started", name)
	result, err := run(s.ctx)
	if err != nil {
		log.Printf("job %s failed: err=%v", name, err)
		return
	}
	if result.Skipped {
		log.Printf("job %s skipped: reason=%s", name, result.SkipReason)
		return
	}
	log.Printf(
		"job %s finished: scanned=%d updated_stats=%d updated_cache_entries=%d redis_members=%d duration=%s",
		name,
		result.Scanned,
		result.UpdatedStats,
		result.UpdatedCacheEntries,
		result.RedisMembers,
		result.Duration(),
	)
}

func normalizeOptions(options SchedulerOptions) SchedulerOptions {
	if options.VideoStatsReconcileInterval <= 0 {
		options.VideoStatsReconcileInterval = defaultVideoStatsReconcileInterval
	}
	if options.HotFeedDirtyRefreshInterval <= 0 {
		options.HotFeedDirtyRefreshInterval = defaultHotFeedDirtyRefreshInterval
	}
	if options.HotFeedRebuildInterval <= 0 {
		options.HotFeedRebuildInterval = defaultHotFeedRebuildInterval
	}
	if options.LockTTL <= 0 {
		options.LockTTL = defaultJobLockTTL
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultJobBatchSize
	}
	if options.HomeHotMaxEntries <= 0 {
		options.HomeHotMaxEntries = defaultHomeHotMaxEntries
	}
	return options
}

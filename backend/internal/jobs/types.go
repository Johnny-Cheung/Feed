package jobs

import "time"

const (
	homeHotFeedKey             = "feed:home:hot"
	videoStatsReconcileLockKey = "lock:job:video-stats-reconcile"
	hotFeedDirtyRefreshLockKey = "lock:job:hot-feed-dirty-refresh"
	hotFeedRebuildLockKey      = "lock:job:hot-feed-rebuild"
)

type SchedulerOptions struct {
	Enabled                     bool
	RunOnStart                  bool
	VideoStatsReconcileInterval time.Duration
	HotFeedDirtyRefreshInterval time.Duration
	HotFeedRebuildInterval      time.Duration
	LockTTL                     time.Duration
	BatchSize                   int
	HomeHotMaxEntries           int
}

type JobResult struct {
	Name                string
	StartedAt           time.Time
	FinishedAt          time.Time
	Scanned             int
	UpdatedStats        int
	UpdatedCacheEntries int
	RedisMembers        int
	Skipped             bool
	SkipReason          string
}

func (r *JobResult) finish() {
	r.FinishedAt = time.Now().UTC()
}

func (r JobResult) Duration() time.Duration {
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt)
}

type HotFeedRef struct {
	VideoID  uint64
	HotScore float64
}

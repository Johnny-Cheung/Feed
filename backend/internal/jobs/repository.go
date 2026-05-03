package jobs

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	redisZAddChunkSize = 1000
)

type Repository struct {
	db    *gorm.DB
	redis *redis.Client
}

type visibleVideo struct {
	ID          uint64
	PublishedAt time.Time
}

type videoStatsCounts struct {
	LikeCount     uint32
	CommentCount  uint32
	FavoriteCount uint32
}

type countRow struct {
	VideoID uint64
	Count   int64
}

func NewRepository(db *gorm.DB, redisClient *redis.Client) *Repository {
	return &Repository{
		db:    db,
		redis: redisClient,
	}
}

func (r *Repository) ListVisibleVideosAfterID(ctx context.Context, lastID uint64, limit int) ([]visibleVideo, error) {
	if limit <= 0 {
		return nil, nil
	}

	var videos []visibleVideo
	if err := r.db.WithContext(ctx).
		Model(&model.Video{}).
		Select("id, published_at").
		Where("id > ? AND status = ?", lastID, model.VideoStatusPublished).
		Order("id ASC").
		Limit(limit).
		Scan(&videos).Error; err != nil {
		return nil, fmt.Errorf("list visible videos after id: %w", err)
	}

	return videos, nil
}

func (r *Repository) LoadVisibleVideosByIDs(ctx context.Context, videoIDs []uint64) (map[uint64]visibleVideo, error) {
	result := make(map[uint64]visibleVideo, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}

	var videos []visibleVideo
	if err := r.db.WithContext(ctx).
		Model(&model.Video{}).
		Select("id, published_at").
		Where("id IN ? AND status = ?", videoIDs, model.VideoStatusPublished).
		Scan(&videos).Error; err != nil {
		return nil, fmt.Errorf("load visible videos by ids: %w", err)
	}

	for _, video := range videos {
		result[video.ID] = video
	}
	return result, nil
}

func (r *Repository) LoadVideoStatsCounts(ctx context.Context, videoIDs []uint64) (map[uint64]videoStatsCounts, error) {
	counts := make(map[uint64]videoStatsCounts, len(videoIDs))
	if len(videoIDs) == 0 {
		return counts, nil
	}
	for _, videoID := range videoIDs {
		counts[videoID] = videoStatsCounts{}
	}

	likeCounts, err := r.countByVideoID(ctx, &model.VideoLike{}, videoIDs, "")
	if err != nil {
		return nil, fmt.Errorf("load like counts: %w", err)
	}
	for videoID, count := range likeCounts {
		current := counts[videoID]
		current.LikeCount = count
		counts[videoID] = current
	}

	favoriteCounts, err := r.countByVideoID(ctx, &model.VideoFavorite{}, videoIDs, "")
	if err != nil {
		return nil, fmt.Errorf("load favorite counts: %w", err)
	}
	for videoID, count := range favoriteCounts {
		current := counts[videoID]
		current.FavoriteCount = count
		counts[videoID] = current
	}

	commentCounts, err := r.countByVideoID(ctx, &model.Comment{}, videoIDs, "status = ?")
	if err != nil {
		return nil, fmt.Errorf("load comment counts: %w", err)
	}
	for videoID, count := range commentCounts {
		current := counts[videoID]
		current.CommentCount = count
		counts[videoID] = current
	}

	return counts, nil
}

func (r *Repository) countByVideoID(ctx context.Context, modelValue interface{}, videoIDs []uint64, extraWhere string) (map[uint64]uint32, error) {
	rows := make([]countRow, 0)
	query := r.db.WithContext(ctx).
		Model(modelValue).
		Select("video_id, COUNT(*) AS count").
		Where("video_id IN ?", videoIDs)

	if extraWhere != "" {
		query = query.Where(extraWhere, model.CommentStatusNormal)
	}

	if err := query.
		Group("video_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	counts := make(map[uint64]uint32, len(rows))
	for _, row := range rows {
		counts[row.VideoID] = clampToUint32(row.Count)
	}
	return counts, nil
}

func (r *Repository) UpsertVideoStatsBatch(ctx context.Context, stats []model.VideoStats) (int, error) {
	if len(stats) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	for i := range stats {
		if stats[i].CreatedAt.IsZero() {
			stats[i].CreatedAt = now
		}
		stats[i].UpdatedAt = now
	}

	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "video_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"like_count",
				"comment_count",
				"favorite_count",
				"hot_score",
				"updated_at",
			}),
		}).
		CreateInBatches(stats, len(stats)).Error; err != nil {
		return 0, fmt.Errorf("upsert video stats batch: %w", err)
	}

	return len(stats), nil
}

func (r *Repository) ReplaceHomeHotFeed(ctx context.Context, refs []HotFeedRef, maxEntries int) (int, error) {
	if r.redis == nil {
		return 0, nil
	}
	if maxEntries <= 0 {
		maxEntries = defaultHomeHotMaxEntries
	}

	refs = topHotFeedRefs(refs, maxEntries)

	pipe := r.redis.TxPipeline()
	pipe.Del(ctx, homeHotFeedKey)

	for start := 0; start < len(refs); start += redisZAddChunkSize {
		end := start + redisZAddChunkSize
		if end > len(refs) {
			end = len(refs)
		}

		zs := make([]redis.Z, 0, end-start)
		for _, ref := range refs[start:end] {
			zs = append(zs, redis.Z{
				Score:  ref.HotScore,
				Member: strconv.FormatUint(ref.VideoID, 10),
			})
		}
		pipe.ZAdd(ctx, homeHotFeedKey, zs...)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("replace home hot feed: %w", err)
	}

	return len(refs), nil
}

func (r *Repository) UpdateHomeHotFeed(ctx context.Context, refs []HotFeedRef, removeVideoIDs []uint64, maxEntries int) (int, error) {
	if r.redis == nil {
		return 0, nil
	}
	if len(refs) == 0 && len(removeVideoIDs) == 0 {
		return 0, nil
	}
	if maxEntries <= 0 {
		maxEntries = defaultHomeHotMaxEntries
	}

	pipe := r.redis.Pipeline()
	for start := 0; start < len(removeVideoIDs); start += redisZAddChunkSize {
		end := start + redisZAddChunkSize
		if end > len(removeVideoIDs) {
			end = len(removeVideoIDs)
		}

		members := make([]interface{}, 0, end-start)
		for _, videoID := range removeVideoIDs[start:end] {
			if videoID == 0 {
				continue
			}
			members = append(members, strconv.FormatUint(videoID, 10))
		}
		if len(members) > 0 {
			pipe.ZRem(ctx, homeHotFeedKey, members...)
		}
	}

	for start := 0; start < len(refs); start += redisZAddChunkSize {
		end := start + redisZAddChunkSize
		if end > len(refs) {
			end = len(refs)
		}

		zs := make([]redis.Z, 0, end-start)
		for _, ref := range refs[start:end] {
			if ref.VideoID == 0 {
				continue
			}
			zs = append(zs, redis.Z{
				Score:  ref.HotScore,
				Member: strconv.FormatUint(ref.VideoID, 10),
			})
		}
		if len(zs) > 0 {
			pipe.ZAdd(ctx, homeHotFeedKey, zs...)
		}
	}
	pipe.ZRemRangeByRank(ctx, homeHotFeedKey, 0, -int64(maxEntries)-1)

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("update home hot feed: %w", err)
	}
	return len(refs), nil
}

func topHotFeedRefs(refs []HotFeedRef, maxEntries int) []HotFeedRef {
	if maxEntries <= 0 || len(refs) <= maxEntries {
		return refs
	}

	sortedRefs := append([]HotFeedRef(nil), refs...)
	sort.Slice(sortedRefs, func(i, j int) bool {
		if sortedRefs[i].HotScore == sortedRefs[j].HotScore {
			return sortedRefs[i].VideoID > sortedRefs[j].VideoID
		}
		return sortedRefs[i].HotScore > sortedRefs[j].HotScore
	})
	return sortedRefs[:maxEntries]
}

func clampToUint32(count int64) uint32 {
	if count <= 0 {
		return 0
	}
	if count >= math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(count)
}

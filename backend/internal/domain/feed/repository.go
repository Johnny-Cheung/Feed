package feed

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"feed-backend/internal/model"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	homeHotFeedKey            = "feed:home:hot"
	defaultHomeHotMaxEntries  = 10000
	redisHomeHotZAddChunkSize = 1000
)

// Repository 负责首页热榜流需要的数据访问：
// - Redis 热榜候选读取
// - MySQL 热榜回源
// - 视频/作者/统计/关系状态批量查询
//
// 教学理解：
// Repository 是“取数据的人”，不负责决定业务策略。
// 它不决定：
// - 先走 Redis 还是 MySQL
// - 什么时候回源
// - 如何组装最终响应
// 这些都交给 Service。
type Repository struct {
	db                *gorm.DB
	redis             *redis.Client
	homeHotMaxEntries int
}

func NewRepository(db *gorm.DB, redisClient *redis.Client, homeHotMaxEntries int) *Repository {
	if homeHotMaxEntries <= 0 {
		homeHotMaxEntries = defaultHomeHotMaxEntries
	}
	return &Repository{
		db:                db,
		redis:             redisClient,
		homeHotMaxEntries: homeHotMaxEntries,
	}
}

// LoadHomeHotRefsFromRedis 从 Redis ZSET 读取首页热榜候选。
// 当前实现先按 score 从 Redis 取一批，再在应用层用 id 做二次过滤和排序。
func (r *Repository) LoadHomeHotRefsFromRedis(ctx context.Context, cursor *hotCursor, count int) ([]HotFeedRef, error) {
	// Redis 客户端不存在，或者 count 非法时，直接返回空结果。
	if r.redis == nil || count <= 0 {
		return nil, nil
	}

	// 首页热榜存的是 ZSET：
	// - member = video_id
	// - score = hot_score
	// 这里用 ZRevRangeByScoreWithScores 取“高分在前”的候选。
	query := &redis.ZRangeBy{
		Max:   "+inf",
		Min:   "-inf",
		Count: int64(count),
	}
	if cursor != nil {
		// 如果不是第一页，就把 score 上界收紧到 cursor.score。
		query.Max = strconv.FormatFloat(cursor.Score, 'f', -1, 64)
	}

	zs, err := r.redis.ZRevRangeByScoreWithScores(ctx, homeHotFeedKey, query).Result()
	if err != nil {
		return nil, fmt.Errorf("load home hot refs from redis: %w", err)
	}

	refs := make([]HotFeedRef, 0, len(zs))
	seen := make(map[uint64]struct{}, len(zs))
	for _, z := range zs {
		// Redis 里 member 是 interface{}，这里统一转成 uint64 的 video_id。
		videoID, ok := parseRedisMemberToUint64(z.Member)
		if !ok {
			continue
		}

		ref := HotFeedRef{
			VideoID:  videoID,
			HotScore: z.Score,
		}
		if !isAfterHotCursor(ref, cursor) {
			continue
		}
		if _, exists := seen[videoID]; exists {
			continue
		}

		// 去重后再收集，避免同一个视频重复返回。
		seen[videoID] = struct{}{}
		refs = append(refs, ref)
	}

	// 因为 Redis 只按 score 做了主过滤，应用层再按 score/id 规则补一次稳定排序。
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].HotScore == refs[j].HotScore {
			return refs[i].VideoID > refs[j].VideoID
		}
		return refs[i].HotScore > refs[j].HotScore
	})

	if len(refs) > count {
		refs = refs[:count]
	}

	return refs, nil
}

// LoadHomeHotRefsFromMySQL 在 Redis 不可用或缓存为空时回源 MySQL。
func (r *Repository) LoadHomeHotRefsFromMySQL(ctx context.Context, cursor *hotCursor, count int) ([]HotFeedRef, error) {
	// 第七阶段里，MySQL 回源承担两个职责：
	// 1. Redis 没数据时兜底
	// 2. Redis 太脏时给出更精确的一页结果
	if count <= 0 {
		return nil, nil
	}

	var refs []HotFeedRef
	// 这里从 videos + video_stats 联表取首页排序所需的最小信息：
	// - video_id
	// - hot_score
	query := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id AS video_id, COALESCE(vs.hot_score, 0) AS hot_score").
		Joins("LEFT JOIN video_stats AS vs ON vs.video_id = v.id").
		Where("v.status = ?", model.VideoStatusPublished).
		Where("v.deleted_at IS NULL")

	if cursor != nil {
		// MySQL 分页条件必须与首页排序规则完全一致。
		query = query.Where(
			"(COALESCE(vs.hot_score, 0) < ?) OR (COALESCE(vs.hot_score, 0) = ? AND v.id < ?)",
			cursor.Score,
			cursor.Score,
			cursor.ID,
		)
	}

	if err := query.
		Order("COALESCE(vs.hot_score, 0) DESC").
		Order("v.id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("load home hot refs from mysql: %w", err)
	}

	return refs, nil
}

// LoadFollowingRefsFromMySQL 直接从 MySQL 查询关注流候选。
// 阶段八的关注流不依赖 Redis，直接按“关注关系 + 发布时间”做时间流分页。
func (r *Repository) LoadFollowingRefsFromMySQL(ctx context.Context, viewerUserID uint64, cursor *timeCursor, count int) ([]FollowingFeedRef, error) {
	// 为什么关注流直接查 MySQL？
	// 因为当前阶段的关注流是“按登录用户个性化过滤”的：
	// - 先看我关注了谁
	// - 再看这些人发了什么
	// 这类查询天然带 user_id 条件，不像首页热榜那样适合直接放在一个全局 Redis ZSET 里。
	if count <= 0 {
		return nil, nil
	}

	var refs []FollowingFeedRef
	// 这里的 join 含义是：
	// - videos v：视频主表
	// - user_follows f：关注关系表
	// 通过 f.follow_user_id = v.author_id，把“我关注的人”关联到“这些人发的视频”。
	query := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id AS video_id, v.published_at AS published_at").
		Joins("JOIN user_follows AS f ON f.follow_user_id = v.author_id").
		Where("f.user_id = ?", viewerUserID).
		Where("v.status = ?", model.VideoStatusPublished).
		Where("v.deleted_at IS NULL")

	if cursor != nil {
		// 时间流分页条件必须和排序规则严格对应：
		// published_at DESC, id DESC
		// 所以下一页条件就是：
		// - 时间更早
		// - 或者时间相同但 ID 更小
		query = query.Where(
			"(v.published_at < ?) OR (v.published_at = ? AND v.id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("v.published_at DESC").
		Order("v.id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("load following refs from mysql: %w", err)
	}

	// 最终返回的是“候选引用”，不是完整 VideoCard。
	// 完整卡片要留给 Service 统一批量组装。
	return refs, nil
}

// RebuildHomeHotCache 把当前所有公开视频按热度重建回 Redis。
// 当前项目阶段里，Redis 只是热榜缓存，所以重建失败不会影响主请求返回。
func (r *Repository) RebuildHomeHotCache(ctx context.Context) error {
	// 这一层只负责“怎么重建”，不负责“什么时候要重建”。
	if r.redis == nil {
		return nil
	}

	refs, err := r.listTopVisibleHotRefs(ctx, r.homeHotMaxEntries)
	if err != nil {
		return err
	}

	pipe := r.redis.TxPipeline()
	// 先删旧热榜，再把全量结果重新写回去。
	pipe.Del(ctx, homeHotFeedKey)
	for start := 0; start < len(refs); start += redisHomeHotZAddChunkSize {
		end := start + redisHomeHotZAddChunkSize
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
		return fmt.Errorf("rebuild home hot cache: %w", err)
	}

	return nil
}

func (r *Repository) listTopVisibleHotRefs(ctx context.Context, limit int) ([]HotFeedRef, error) {
	if limit <= 0 {
		return nil, nil
	}

	// 这是“全量重建热榜”时的数据来源。
	var refs []HotFeedRef
	if err := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id AS video_id, COALESCE(vs.hot_score, 0) AS hot_score").
		Joins("LEFT JOIN video_stats AS vs ON vs.video_id = v.id").
		Where("v.status = ?", model.VideoStatusPublished).
		Where("v.deleted_at IS NULL").
		Order("COALESCE(vs.hot_score, 0) DESC").
		Order("v.id DESC").
		Limit(limit).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list top visible hot refs: %w", err)
	}

	return refs, nil
}

func (r *Repository) GetVisibleVideosByIDs(ctx context.Context, videoIDs []uint64) ([]model.Video, error) {
	// 这里再次带上“status=published 且未删除”的限制，
	// 是为了避免缓存里混进不可见视频时直接泄露给前端。
	if len(videoIDs) == 0 {
		return nil, nil
	}

	var videos []model.Video
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND status = ?", videoIDs, model.VideoStatusPublished).
		Find(&videos).Error; err != nil {
		return nil, fmt.Errorf("get visible videos by ids: %w", err)
	}

	return videos, nil
}

func (r *Repository) GetUsersByIDs(ctx context.Context, userIDs []uint64) ([]model.User, error) {
	// 首页是列表场景，所以一律走批量查询。
	if len(userIDs) == 0 {
		return nil, nil
	}

	var users []model.User
	if err := r.db.WithContext(ctx).
		Where("id IN ?", userIDs).
		Find(&users).Error; err != nil {
		return nil, fmt.Errorf("get users by ids: %w", err)
	}

	return users, nil
}

func (r *Repository) GetVideoStatsByIDs(ctx context.Context, videoIDs []uint64) ([]model.VideoStats, error) {
	// 统计也是一页视频一起查，而不是一条视频查一次。
	if len(videoIDs) == 0 {
		return nil, nil
	}

	var stats []model.VideoStats
	if err := r.db.WithContext(ctx).
		Where("video_id IN ?", videoIDs).
		Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("get video stats by ids: %w", err)
	}

	return stats, nil
}

func (r *Repository) GetLikedVideoIDs(ctx context.Context, viewerUserID uint64, videoIDs []uint64) (map[uint64]struct{}, error) {
	// 返回 map[video_id]struct{}，是为了后面 O(1) 判断某条视频是否已点赞。
	return r.getUint64Set(
		ctx,
		"video_likes",
		"video_id",
		"user_id = ? AND video_id IN ?",
		viewerUserID,
		videoIDs,
	)
}

func (r *Repository) GetFavoritedVideoIDs(ctx context.Context, viewerUserID uint64, videoIDs []uint64) (map[uint64]struct{}, error) {
	// 收藏状态和点赞状态类似，都是“批量查 + O(1) 命中判断”。
	return r.getUint64Set(
		ctx,
		"video_favorites",
		"video_id",
		"user_id = ? AND video_id IN ?",
		viewerUserID,
		videoIDs,
	)
}

func (r *Repository) GetFollowedAuthorIDs(ctx context.Context, viewerUserID uint64, authorIDs []uint64) (map[uint64]struct{}, error) {
	// 这里查的是“当前查看者关注了哪些作者”。
	return r.getUint64Set(
		ctx,
		"user_follows",
		"follow_user_id",
		"user_id = ? AND follow_user_id IN ?",
		viewerUserID,
		authorIDs,
	)
}

func (r *Repository) getUint64Set(ctx context.Context, tableName, column, query string, args ...interface{}) (map[uint64]struct{}, error) {
	// 这个小工具函数统一了“批量查一列 ID，然后转成 set”的模式。
	result := make(map[uint64]struct{})
	if len(args) == 0 {
		return result, nil
	}

	var values []uint64
	if err := r.db.WithContext(ctx).
		Table(tableName).
		Select(column).
		Where(query, args...).
		Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("get uint64 set from %s: %w", tableName, err)
	}

	for _, value := range values {
		result[value] = struct{}{}
	}

	return result, nil
}

func parseRedisMemberToUint64(member interface{}) (uint64, bool) {
	// Redis ZSET member 读出来可能是 string，也可能是 []byte。
	// 这里统一兼容两种情况。
	switch value := member.(type) {
	case string:
		parsed, err := strconv.ParseUint(value, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseUint(string(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

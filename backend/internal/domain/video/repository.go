package video

import (
	"context"
	"errors"
	"fmt"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/model"

	"gorm.io/gorm"
)

// Repository 负责视频领域和数据库之间的数据访问。
// 这里主要处理：
// 1. videos 主表
// 2. video_stats 初始化与读取
// 3. 详情接口需要的作者/查看者状态查询
//
// 教学理解：
// Repository 的职责是“把业务层想要的数据读出来/写进去”。
// 它不负责：
// - 解析 HTTP
// - 做权限判断
// - 发 MQ 事件
// 这些都应该留在更上层的 Handler / Service。
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateVideoWithStats 在一个事务里同时创建 videos 和 video_stats。
// 阶段六要求 video_stats 初始化必须同步完成，不能依赖后续异步补写。
func (r *Repository) CreateVideoWithStats(ctx context.Context, video *model.Video) error {
	// 为什么这里要开事务？
	// 因为 videos 和 video_stats 在业务上必须“一起成功”：
	// - 只写入 videos，不写 video_stats，会导致统计表缺失
	// - 只写入 video_stats，不写 videos，更不合理
	// 用事务可以保证这两步要么都成功，要么都回滚。
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(video).Error; err != nil {
			return fmt.Errorf("create video: %w", err)
		}

		stats := &model.VideoStats{
			VideoID: video.ID,
		}
		if err := tx.Create(stats).Error; err != nil {
			return fmt.Errorf("create video stats: %w", err)
		}

		return nil
	})
}

// GetVisibleVideoByID 只返回“对公开读取接口可见”的视频。
// 也就是：
// - status = published
// - 未被软删除
// 这个方法给“公开视频详情”使用，所以条件会更严格。
func (r *Repository) GetVisibleVideoByID(ctx context.Context, videoID uint64) (*model.Video, error) {
	var video model.Video
	err := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", videoID, model.VideoStatusPublished).
		First(&video).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrVideoNotFound
		}
		return nil, fmt.Errorf("get visible video by id: %w", err)
	}

	return &video, nil
}

// GetVideoByID 返回任意“未被软删除”的视频。
// 这个方法主要给作者自己做编辑/删除权限判断时使用。
// 对比上面的 GetVisibleVideoByID：
// - GetVisibleVideoByID 面向公开读接口
// - GetVideoByID 面向作者自己的管理接口
func (r *Repository) GetVideoByID(ctx context.Context, videoID uint64) (*model.Video, error) {
	var video model.Video
	err := r.db.WithContext(ctx).First(&video, videoID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrVideoNotFound
		}
		return nil, fmt.Errorf("get video by id: %w", err)
	}

	return &video, nil
}

// GetUserByID 用于加载作者信息。
func (r *Repository) GetUserByID(ctx context.Context, userID uint64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).First(&user, userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return &user, nil
}

// GetVideoStatsByVideoID 读取视频统计。
// 如果统计行不存在，这里退化成零值返回，避免详情接口直接 500。
// 这属于一种“容错式读取”：
// 详情页更希望先返回可用结果，而不是因为统计缺失直接整页失败。
func (r *Repository) GetVideoStatsByVideoID(ctx context.Context, videoID uint64) (*model.VideoStats, error) {
	var stats model.VideoStats
	err := r.db.WithContext(ctx).First(&stats, "video_id = ?", videoID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.VideoStats{VideoID: videoID}, nil
		}
		return nil, fmt.Errorf("get video stats by video id: %w", err)
	}

	return &stats, nil
}

// UpdateVideoByID 更新视频的部分字段。
// updates 用 map 的好处是“只更新传进来的列”。
// 例如这次只改标题，就不需要把整条 Video 结构体重新写回数据库。
func (r *Repository) UpdateVideoByID(ctx context.Context, videoID uint64, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	if err := r.db.WithContext(ctx).
		Model(&model.Video{}).
		Where("id = ?", videoID).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("update video by id: %w", err)
	}

	return nil
}

// SoftDeleteVideoByID 对视频执行软删除。
func (r *Repository) SoftDeleteVideoByID(ctx context.Context, videoID uint64) error {
	if err := r.db.WithContext(ctx).Delete(&model.Video{}, videoID).Error; err != nil {
		return fmt.Errorf("soft delete video by id: %w", err)
	}
	return nil
}

// ExistsVideoLike 判断当前查看者是否点赞过该视频。
func (r *Repository) ExistsVideoLike(ctx context.Context, userID, videoID uint64) (bool, error) {
	return r.exists(ctx, &model.VideoLike{}, "user_id = ? AND video_id = ?", userID, videoID)
}

// ExistsVideoFavorite 判断当前查看者是否收藏过该视频。
func (r *Repository) ExistsVideoFavorite(ctx context.Context, userID, videoID uint64) (bool, error) {
	return r.exists(ctx, &model.VideoFavorite{}, "user_id = ? AND video_id = ?", userID, videoID)
}

// ExistsUserFollow 判断当前查看者是否关注了视频作者。
func (r *Repository) ExistsUserFollow(ctx context.Context, userID, followUserID uint64) (bool, error) {
	return r.exists(ctx, &model.UserFollow{}, "user_id = ? AND follow_user_id = ?", userID, followUserID)
}

func (r *Repository) exists(ctx context.Context, modelValue interface{}, query string, args ...interface{}) (bool, error) {
	var count int64
	// 这里统一用 count > 0 判断关系是否存在。
	// 对初学者来说，这样比先查整行记录再判断更直观。
	if err := r.db.WithContext(ctx).Model(modelValue).Where(query, args...).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check relation exists: %w", err)
	}
	return count > 0, nil
}
